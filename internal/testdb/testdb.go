// Package testdb provides a shared test-time Postgres provisioning helper used
// by the integration tests across modules (account, manualcharge, telemetry).
//
// Provisioning policy (ai/go-conventions.md §persistence):
//   - When DATABASE_URL is set AND reachable, it is used as-is (managed/CI
//     Postgres). goose records applied versions in goose_db_version, so
//     re-running against an already-migrated DB is a no-op.
//   - Otherwise (DATABASE_URL unset, malformed, or unreachable — including the
//     Makefile's `.env` include quirk where quoted DSNs arrive with literal
//     quote characters), a disposable `postgres:16-alpine` container is started
//     via testcontainers-go, migrations are applied, and the DSN is returned.
//
// IMPORTANT: import this package ONLY from `_test.go` files. It transitively
// pulls testcontainers-go and pressly/goose/v3 — never ship these in the
// deployed binary. The `internal/` path prevents external repos from importing
// it; the team must additionally avoid production imports. As long as no
// non-test file imports testdb, `go build ./cmd/...` will not include it.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"

	// Register the pgx driver as "pgx" for database/sql, which goose uses.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// PostgresImage is pinned (no :latest) for reproducible test runs. PG 16 ships
// gen_random_uuid() in core (no pgcrypto), which several migrations rely on.
const PostgresImage = "postgres:16-alpine"

// Result is what Provision returns. Container is non-nil when a testcontainer
// was started; the caller MUST Terminate it (typically in TestMain after m.Run).
type Result struct {
	DSN       string
	Container *postgres.PostgresContainer
}

// Provision returns a DSN ready for pgxpool.New, with the caller's goose
// migrations already applied. migrationsFS must be an fs.FS already rooted at
// the migrations directory (typically `fs.Sub(embedMigrations, "db/migrations")`).
//
// On success the caller owns any started container; on failure Provision
// returns a non-nil error and has already cleaned up anything it started.
func Provision(ctx context.Context, migrationsFS fs.FS) (Result, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn != "" {
		if err := retry(5, 2*time.Second, func() error { return applyMigrations(ctx, dsn, migrationsFS) }); err != nil {
			log.Printf("testdb: DATABASE_URL not usable (%v); provisioning testcontainer", err)
			dsn = ""
		}
	}

	var container *postgres.PostgresContainer
	if dsn == "" {
		c, err := postgres.Run(ctx, PostgresImage,
			postgres.WithDatabase("test"),
			postgres.WithUsername("test"),
			postgres.WithPassword("test"),
		)
		if err != nil {
			return Result{}, fmt.Errorf("testdb: start postgres container: %w", err)
		}
		container = c

		var cErr error
		dsn, cErr = c.ConnectionString(ctx, "sslmode=disable")
		if cErr != nil {
			_ = c.Terminate(context.Background())
			return Result{}, fmt.Errorf("testdb: container connection string: %w", cErr)
		}

		// Postgres may reset connections briefly after the "ready" log line;
		// retry so TestMain is robust on slow/loaded hosts.
		if err := retry(5, 2*time.Second, func() error { return applyMigrations(ctx, dsn, migrationsFS) }); err != nil {
			_ = c.Terminate(context.Background())
			return Result{}, fmt.Errorf("testdb: apply migrations: %w", err)
		}
	}

	return Result{DSN: dsn, Container: container}, nil
}

// Terminate cleans up the container if one was started. Safe to call with a
// nil Container (no-op).
func (r Result) Terminate(ctx context.Context) error {
	if r.Container == nil {
		return nil
	}
	return r.Container.Terminate(ctx)
}

func applyMigrations(ctx context.Context, dsn string, migrationsFS fs.FS) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open sql db: %w", err)
	}
	defer db.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrationsFS)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	defer func() { _ = provider.Close() }()

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// retry calls fn up to attempts times, sleeping delay between attempts. It
// returns the last error once attempts are exhausted.
func retry(attempts int, delay time.Duration, fn func() error) error {
	var err error
	for i := range attempts {
		if err = fn(); err == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	return err
}