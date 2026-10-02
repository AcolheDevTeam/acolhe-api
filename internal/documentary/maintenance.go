package documentary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Inventory struct {
	Current  map[string]int `json:"current"`
	Versions map[string]int `json:"versions"`
	Legacy   int            `json:"legacy"`
	Failures int            `json:"failures"`
}
type MaintenanceResult struct {
	Migrated  int       `json:"migrated"`
	Inventory Inventory `json:"inventory"`
}

// InventoryContents verifies every envelope without exposing text or text hashes.
// This is an administrative operation: use a restricted, privileged database role.
type inventoryReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func InventoryContents(ctx context.Context, pool *pgxpool.Pool, keys *Keyring) (Inventory, error) {
	return inventoryContents(ctx, pool, keys)
}
func inventoryContents(ctx context.Context, pool inventoryReader, keys *Keyring) (Inventory, error) {
	var privileged bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&privileged); err != nil {
		return Inventory{}, err
	}
	if !privileged {
		return Inventory{}, errors.New("inventário exige uma conexão administrativa sem filtragem RLS")
	}
	out := Inventory{Current: map[string]int{}, Versions: map[string]int{}}
	err := pool.QueryRow(ctx, `SELECT count(*) FROM documentary_record_legacy`).Scan(&out.Legacy)
	if err != nil {
		return out, err
	}
	for _, versions := range []bool{false, true} {
		query := `SELECT d.id,d.organization_id,d.author_id,d.patient_id,d.category,d.revision,d.content_encrypted FROM documentary_record d ORDER BY d.id`
		if versions {
			query = `SELECT d.id,v.organization_id,v.author_id,d.patient_id,d.category,v.revision,v.content_encrypted FROM documentary_record_version v JOIN documentary_record d ON d.id=v.record_id ORDER BY v.id`
		}
		rows, err := pool.Query(ctx, query)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var n Notebook
			var org, author uuid.UUID
			var raw []byte
			if err = rows.Scan(&n.ID, &org, &author, &n.PatientID, &n.Category, &n.Revision, &raw); err != nil {
				rows.Close()
				return out, err
			}
			var e envelope
			if json.Unmarshal(raw, &e) != nil {
				out.Failures++
				continue
			}
			counts := out.Current
			if versions {
				counts = out.Versions
			}
			counts[e.KeyID]++
			if _, err = keys.Decrypt(raw, aad(org, author, n, n.Revision)); err != nil {
				out.Failures++
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// Reencrypt processes bounded transactions. Already migrated envelopes are excluded
// on restart. Row locks serialize against clinical saves; a session lock excludes
// other maintenance jobs. No clinical timestamp or revision is changed.
func Reencrypt(ctx context.Context, pool *pgxpool.Pool, keys *Keyring, source, target string, batch, maxBatches int) (MaintenanceResult, error) {
	var result MaintenanceResult
	var privileged bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&privileged); err != nil {
		return result, err
	}
	if !privileged {
		return result, errors.New("manutenção exige uma conexão administrativa sem filtragem RLS")
	}
	if keys == nil || keys.keys[source] == nil || keys.keys[target] == nil || source == target || target != keys.active || batch < 1 || batch > 1000 || maxBatches < 1 {
		return result, ErrInput
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Release()
	var locked bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(73492381001)`).Scan(&locked)
	if err != nil {
		return result, err
	}
	if !locked {
		return result, errors.New("manutenção documental já está em execução")
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(73492381001)`) }()
	for _, versions := range []bool{false, true} {
		for b := 0; b < maxBatches; b++ {
			count, err := rotateBatch(ctx, conn, keys, source, batch, versions)
			if err != nil {
				result.Inventory.Failures++
				return result, err
			}
			result.Migrated += count
			if count == 0 {
				break
			}
		}
	}
	result.Inventory, err = inventoryContents(ctx, conn, keys)
	if err != nil {
		return result, err
	}
	if result.Inventory.Failures > 0 {
		return result, ErrCrypto
	}
	if result.Inventory.Legacy > 0 {
		return result, errors.New("registros legados exigem inventário de origem antes de concluir a rotação")
	}
	if result.Inventory.Current[source]+result.Inventory.Versions[source] > 0 {
		return result, errors.New("migração parcial: execute novamente para processar os itens restantes")
	}
	return result, nil
}
func rotateBatch(ctx context.Context, conn *pgxpool.Conn, keys *Keyring, source string, batch int, versions bool) (int, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := `SELECT d.id,d.id,d.organization_id,d.author_id,d.patient_id,d.category,d.revision,d.content_encrypted FROM documentary_record d WHERE convert_from(d.content_encrypted,'UTF8')::jsonb->>'keyId'=$1 ORDER BY d.id LIMIT $2 FOR UPDATE OF d SKIP LOCKED`
	table := "documentary_record"
	if versions {
		table = "documentary_record_version"
		query = `SELECT v.id,d.id,v.organization_id,v.author_id,d.patient_id,d.category,v.revision,v.content_encrypted FROM documentary_record_version v JOIN documentary_record d ON d.id=v.record_id WHERE convert_from(v.content_encrypted,'UTF8')::jsonb->>'keyId'=$1 ORDER BY v.id LIMIT $2 FOR UPDATE OF v SKIP LOCKED`
	}
	rows, err := tx.Query(ctx, query, source, batch)
	if err != nil {
		return 0, err
	}
	type item struct {
		id, org, author uuid.UUID
		n               Notebook
		raw             []byte
	}
	items := []item{}
	for rows.Next() {
		var i item
		if err = rows.Scan(&i.id, &i.n.ID, &i.org, &i.author, &i.n.PatientID, &i.n.Category, &i.n.Revision, &i.raw); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, i := range items {
		associated := aad(i.org, i.author, i.n, i.n.Revision)
		plain, err := keys.Decrypt(i.raw, associated)
		if err != nil {
			return 0, err
		}
		encrypted, err := keys.Encrypt(plain, associated)
		if err != nil {
			return 0, err
		}
		verified, err := keys.Decrypt(encrypted, associated)
		if err != nil || verified != plain {
			return 0, ErrCrypto
		}
		_, err = tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET content_encrypted=$1 WHERE id=$2 AND organization_id=$3 AND author_id=$4`, pgx.Identifier{table}.Sanitize()), encrypted, i.id, i.org, i.author)
		if err != nil {
			return 0, err
		}
	}
	if len(items) > 0 {
		_, err = tx.Exec(ctx, `INSERT INTO documentary_maintenance_audit(source_key_id,target_key_id,item_count) VALUES($1,$2,$3)`, source, keys.active, len(items))
		if err != nil {
			return 0, err
		}
	}
	return len(items), tx.Commit(ctx)
}
