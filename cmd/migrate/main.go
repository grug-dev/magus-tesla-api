// Command migrate applies the platform's goose migration directories, in
// order, against DATABASE_URL, then exits. It is the "migrate" service's
// ENTRYPOINT in the Docker deploy (Dockerfile, compose.yaml) — see
// openspec/changes/platform-add-docker-compose-deploy/design.md, decisions
// D10 and D11 (the amendment section at the end of that file).
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

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("migrate: open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	for _, dir := range cfg.MigrationsDirs {
		if err := applyDir(ctx, db, dir); err != nil {
			log.Fatalf("migrate: %s: %v", dir, err)
		}
	}

	log.Printf("migrate: all %d migration directories applied successfully", len(cfg.MigrationsDirs))
}

// applyDir applies one migration directory with its own goose provider and
// logs how many migrations it ran.
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
func applyDir(ctx context.Context, db *sql.DB, dir string) error {
	log.Printf("migrate: applying %s", dir)

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

	log.Printf("migrate: %s: applied %d migration(s)", dir, len(results))
	return nil
}
