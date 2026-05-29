// Comando de seed: cria uma organização, um usuário psicólogo e pacientes de
// exemplo. Idempotente — usa upsert pelo e-mail. Imprime as credenciais no fim.
//
//	go run ./cmd/seed
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/joycesilva/acolhe-api/internal/auth"
	"github.com/joycesilva/acolhe-api/internal/config"
	"github.com/joycesilva/acolhe-api/internal/db"
)

const (
	seedEmail    = "psi@acolhe.dev"
	seedPassword = "acolhe123"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	// Organização
	var orgID string
	err = pool.QueryRow(ctx,
		`INSERT INTO organization (name, slug)
		 VALUES ('Clínica Demo', 'clinica-demo')
		 ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`).Scan(&orgID)
	if err != nil {
		log.Fatalf("organization: %v", err)
	}

	// Usuário psicólogo
	hash, err := auth.HashPassword(seedPassword)
	if err != nil {
		log.Fatalf("hash: %v", err)
	}
	var userID string
	err = pool.QueryRow(ctx,
		`SELECT id FROM "user" WHERE lower(email) = lower($1)`, seedEmail).Scan(&userID)
	if err != nil {
		// não existe: cria
		err = pool.QueryRow(ctx,
			`INSERT INTO "user" (organization_id, email, password_hash, role)
			 VALUES ($1, $2, $3, 'psychologist') RETURNING id`,
			orgID, seedEmail, hash).Scan(&userID)
		if err != nil {
			log.Fatalf("user insert: %v", err)
		}
	} else {
		// existe: atualiza a senha
		if _, err = pool.Exec(ctx,
			`UPDATE "user" SET password_hash = $2 WHERE id = $1`, userID, hash); err != nil {
			log.Fatalf("user update: %v", err)
		}
	}

	// Perfil do psicólogo
	var psyID string
	err = pool.QueryRow(ctx,
		`INSERT INTO psychologist_profile (user_id, full_name, crp_number, crp_state)
		 VALUES ($1, 'Dra. Demo', '00000', 'SP')
		 ON CONFLICT (crp_number, crp_state) DO UPDATE SET full_name = EXCLUDED.full_name
		 RETURNING id`, userID).Scan(&psyID)
	if err != nil {
		log.Fatalf("psychologist_profile: %v", err)
	}

	// Pacientes de exemplo + vínculo ativo (só se a org ainda não tiver pacientes)
	var existing int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM patient_profile WHERE organization_id = $1`, orgID).Scan(&existing)
	patients := []string{"Ana Souza", "Bruno Lima", "Carla Mendes"}
	for _, name := range patients {
		if existing > 0 {
			break
		}
		var patID string
		err = pool.QueryRow(ctx,
			`INSERT INTO patient_profile (organization_id, full_name, status)
			 VALUES ($1, $2, 'active')
			 RETURNING id`, orgID, name).Scan(&patID)
		if err != nil {
			// provavelmente já existe de um seed anterior; ignora
			continue
		}
		_, _ = pool.Exec(ctx,
			`INSERT INTO patient_relationship (patient_id, psychologist_id, status)
			 VALUES ($1, $2, 'active')`, patID, psyID)
	}

	fmt.Println("✅ Seed concluído.")
	fmt.Println("   Login:", seedEmail)
	fmt.Println("   Senha:", seedPassword)
}
