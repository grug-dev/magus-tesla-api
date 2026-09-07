// Command migrate applies the platform's four goose migration directories, in
// order, against DATABASE_URL, then exits. It is the "migrate" service's
// ENTRYPOINT in the Docker deploy (Dockerfile, compose.yaml) — see
// openspec/changes/platform-add-docker-compose-deploy/design.md, decisions
// D10 and D11 (the amendment section at the end of that file).
//
// Why not the goose CLI: design.md's original plan (D2) was to build
// `github.com/pressly/goose/v3/cmd/goose` in the Docker builder stage, from
// the project's own pinned go.mod version. That build fails —
//
//	go build github.com/pressly/goose/v3/cmd/goose
//	# missing go.sum entry for module providing package ...
//
// — because this project depends on goose as a LIBRARY only. go.sum has no
// entries for the CLI's optional database drivers (clickhouse-go,
// go-sql-driver/mysql, mfridman/xflag, microsoft/go-mssqldb,
// tursodatabase/libsql-client-go, vertica-sql-go, ydb-go-sdk,
// ziutek/mymysql), so the CLI binary cannot compile in this repo. This
// command uses the goose Provider API instead, which needs only the postgres
// driver already imported (the same one internal/testdb uses).
//
// Why not //go:embed: the directive cannot contain ".." path elements, so a
// package can only ever embed its own directory tree — it cannot reach
// another module's migrations folder. internal/testdb.ProvisionDirs documents
// this same restriction. Instead, this command reads the four directories
// from the image filesystem at runtime: the Dockerfile COPYs each module's
// migrations to /migrations/<module>.
//
// Config comes from internal/config.LoadMigration — see that function's doc
// comment for why it is a separate, smaller loader than internal/config.Load.
//
// This file is wiring only, per CLAUDE.md's "cmd/ stays thin" rule: it reads
// config, loops four fixed directories, and reports success or failure
// through the process exit code, which compose's service_completed_successfully
// condition depends on.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	// Register the pgx driver as "pgx" for database/sql, which goose uses.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/cristianpena/magus-tesla-api/internal/config"
)

// moduleDirs are the four migration directories, applied in this exact
// order, mirroring the Makefile's MIGRATIONS_DIRS
// (ai/go-conventions.md §Persistence): all four apply against ONE shared
// goose_db_version table, and charging's backfill migration reads
// telemetry's table, so telemetry must precede charging.
var moduleDirs = []string{"account", "telemetry", "charging", "analytics"}

func main() {
	cfg, err := config.LoadMigration()
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("migrate: open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	for _, name := range moduleDirs {
		dir := cfg.MigrationsRoot + "/" + name
		if err := applyDir(ctx, db, name, dir); err != nil {
			log.Fatalf("migrate: %s (%s): %v", name, dir, err)
		}
	}

	log.Println("migrate: all four migration directories applied successfully")
}

// applyDir applies one module's migration directory with its own goose
// provider and logs how many migrations it ran.
//
// WithAllowOutofOrder(true) is required. It is the Provider API's equivalent
// of the goose CLI's -allow-missing flag, which make migrate-up already
// passes for the same reason: every module applies its own directory
// against ONE shared goose_db_version table, so a directory's own version
// numbers are routinely lower than versions another directory already
// recorded. Without this option, applying telemetry's directory to a
// database that already carries analytics' later-numbered rows is refused
// as out of order. See internal/testdb/testdb.go's applyMigrations, which
// documents and uses the same option for the same reason.
func applyDir(ctx context.Context, db *sql.DB, name, dir string) error {
	log.Printf("migrate: applying %s (%s)", name, dir)

	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir),
		goose.WithAllowOutofOrder(true))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	defer func() { _ = provider.Close() }()

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("goose up: %w", err)
	}

	log.Printf("migrate: %s: applied %d migration(s)", name, len(results))
	return nil
}
