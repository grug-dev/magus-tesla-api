// Command migrate applies the platform's goose migration directories against
// DATABASE_URL, then exits. It is the "migrate" service's ENTRYPOINT in the
// Docker deploy (Dockerfile, compose.yaml).
//
// Each module's migrations are recorded in that module's own version ledger,
// <module>.goose_db_version, so the directories are independent: no module's
// migrations may read another module's schema, and the order they are applied
// in does not change the result. The order below is kept stable only so two
// runs produce comparable logs.
//
// Two ways to run it (T8):
//   - Image default: no MIGRATIONS_DIRS set. It applies MigrationsRoot (default
//     "/migrations", overridable via MIGRATIONS_ROOT) + each of the four module
//     names, in order. This is the path compose.yaml and the Dockerfile use —
//     unchanged.
//   - Local run: set MIGRATIONS_DIRS to a space-separated, ordered list of
//     migration directories, e.g. "internal/account/db/migrations
//     internal/telemetry/db/migrations internal/charging/db/migrations
//     internal/analytics/db/migrations" — the exact layout of a repo checkout,
//     which the image-default layout does not exist in. `make migrate-run`
//     sets this from the Makefile's own MIGRATIONS_DIRS, so there is one
//     source of truth for the order.
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
// this same restriction. Instead, this command reads the directory list
// config.LoadMigration resolved: in the image, the Dockerfile COPYs each
// module's migrations to /migrations/<module>, and in a local checkout,
// MIGRATIONS_DIRS points straight at each internal/<module>/db/migrations.
//
// Config comes from internal/config.LoadMigration — see that function's doc
// comment for why it is a separate, smaller loader than internal/config.Load.
//
// This file is wiring only, per CLAUDE.md's "cmd/ stays thin" rule: it reads
// config, loops the ordered directory list config.LoadMigration resolved,
// and reports success or failure through the process exit code, which
// compose's service_completed_successfully condition depends on. It no
// longer builds any path itself — internal/config.LoadMigration owns that
// (T8).
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

func main() {
	cfg, err := config.LoadMigration()
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// Fail fast on a bad path. os.DirFS on a missing directory does not error
	// here — goose would simply find no migrations and report "applied 0",
	// so a typo in MIGRATIONS_DIRS would look like success and leave the
	// database un-migrated. internal/testdb.ProvisionDirs checks the same way,
	// for the same reason.
	for _, m := range cfg.MigrationsDirs {
		info, err := os.Stat(m.Dir)
		if err != nil {
			log.Fatalf("migrate: migration dir %q: %v", m.Dir, err)
		}
		if !info.IsDir() {
			log.Fatalf("migrate: migration path %q is not a directory", m.Dir)
		}
	}

	ctx := context.Background()
	for _, m := range cfg.MigrationsDirs {
		if err := applyDir(ctx, cfg.DatabaseURL, m); err != nil {
			log.Fatalf("migrate: %s: %v", m.Dir, err)
		}
	}

	log.Printf("migrate: all %d migration directories applied successfully", len(cfg.MigrationsDirs))
}

// applyDir applies one module's migration directory with its own goose provider
// and logs how many migrations it ran.
//
// WithTableName points goose at the module's OWN version ledger, inside the
// Postgres schema that module already owns. Every module having its own ledger
// is what lets two modules use the same version number: with one shared table,
// goose records a number once and skips the second file in silence, reporting
// success. It also means a module's applied-version history is dumped and
// restored together with its schema.
//
// There is deliberately no WithAllowOutofOrder here. It used to be required,
// because every directory wrote to one shared table and a module's own version
// numbers were routinely lower than versions another module had already
// recorded — which goose reads as a migration arriving late. Per-module ledgers
// remove that, so goose's default protection against an actually-late migration
// is back on. Do not reintroduce the option to silence an ordering complaint:
// within one module the complaint is real.
//
// Each directory opens its OWN *sql.DB. goose's Provider.Close() closes the
// database handle it was given, so a single shared handle is closed by the
// first directory and every later one fails with "sql: database is closed".
// internal/testdb.applyMigrations opens one handle per directory for the same
// reason; ProvisionDirs then loops over it. Follow that shape here.
func applyDir(ctx context.Context, dsn string, m config.MigrationDir) error {
	log.Printf("migrate: applying %s into %s", m.Dir, m.VersionTable())

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(m.Dir),
		goose.WithTableName(m.VersionTable()))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	defer func() { _ = provider.Close() }()

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("goose up: %w", err)
	}

	log.Printf("migrate: %s: applied %d migration(s)", m.Dir, len(results))
	return nil
}
