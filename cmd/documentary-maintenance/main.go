// Administrative tool. Never accepts key material through command line arguments.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joycesilva/acolhe-api/internal/documentary"
)

func main() { os.Exit(run()) }
func run() int {
	mode := flag.String("mode", "inventory", "inventory or reencrypt")
	source := flag.String("from", "", "source key identifier")
	target := flag.String("to", "", "destination (must be active)")
	batch := flag.Int("batch-size", 100, "items per transaction (1-1000)")
	batches := flag.Int("max-batches", 100, "maximum batches per table; rerun to resume")
	flag.Parse()
	keys, err := documentary.ParseKeyring(os.Getenv("DOCUMENTARY_ACTIVE_KEY_ID"), os.Getenv("DOCUMENTARY_ENCRYPTION_KEYS"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuração criptográfica inválida")
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "banco indisponível")
		return 1
	}
	defer pool.Close()
	var result any
	switch *mode {
	case "inventory":
		var inv documentary.Inventory
		inv, err = documentary.InventoryContents(ctx, pool, keys)
		result = inv
		if err == nil && (inv.Failures > 0 || inv.Legacy > 0) {
			err = documentary.ErrCrypto
		}
	case "reencrypt":
		result, err = documentary.Reencrypt(ctx, pool, keys, *source, *target, *batch, *batches)
	default:
		fmt.Fprintln(os.Stderr, "modo inválido")
		return 1
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "operação incompleta; confira a configuração, a integridade e os itens restantes antes de retomar")
		return 1
	}
	return 0
}
