// Package testdb provides a shared test-time Postgres provisioning helper used
// by the integration tests across modules (account, charging, telemetry,
// analytics).
//
// Two entry points:
//   - Provision(ctx, fs) — one module's own embedded migrations. What a module
//     whose DB-backed tests touch only its own tables uses.
//   - ProvisionDirs(ctx, dirs...) — several modules' migration DIRECTORIES, for a
//     package whose fixtures span more than one module's schema. Required because
//     //go:embed cannot reach outside its own directory tree; see ProvisionDirs.
//
// Provisioning policy (ai/go-conventions.md §persistence):
//   - When TEST_DATABASE_URL is set AND reachable, it is used as-is (managed/CI
//     Postgres). goose records applied versions in goose_db_version, so
//     re-running against an already-migrated DB is a no-op.
//   - Otherwise (TEST_DATABASE_URL unset, malformed, or unreachable — including the
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
	"errors"
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

// ErrUnavailable wraps the one failure mode that means "this machine cannot give
// us a Postgres at all" — no reachable TEST_DATABASE_URL and no Docker daemon to start
// a container. A TestMain may legitimately treat it as a reason to SKIP its
// DB-backed tests (see internal/telemetry/testdb_test.go).
//
// Every OTHER Provision failure — above all a migration that fails to apply — is
// deliberately NOT wrapped in it, because those mean the schema or the test setup
// is genuinely broken and MUST fail loudly. Skipping on them would let a broken
// migration pass `make check` in silence, which is precisely the trap this
// sentinel exists to prevent. Callers: use errors.Is(err, testdb.ErrUnavailable)
// to skip, and log.Fatal on anything else.
var ErrUnavailable = errors.New("testdb: no Postgres available (no reachable TEST_DATABASE_URL and no Docker daemon)")

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
	return provision(ctx, func(ctx context.Context, dsn string) error {
		return applyMigrations(ctx, dsn, migrationsFS)
	})
}

// ProvisionDirs is Provision for a package whose DB-backed tests span MORE THAN
// ONE module's schema — for example internal/analytics, whose Recalculate reads
// telemetry's vehicle_snapshots and charging's manual_charge_entries and writes
// its own vehicle_metrics, so its fixtures need all three schemas in one
// database.
//
// Why directories rather than an embed.FS: the //go:embed DIRECTIVE may not
// contain ".." path elements, so a package can only ever embed its own
// migrations — internal/analytics cannot embed internal/telemetry's. That is a
// restriction on the directive, not on the filesystem, and `go test` always runs
// a test binary with its own package directory as the working directory, so a
// relative path like "../telemetry/db/migrations" resolves reliably from a
// _test.go file. Each directory is read with os.DirFS at call time.
//
// Directories are applied IN THE ORDER GIVEN, each with its own goose provider
// against the same database — exactly what the Makefile's migrate-up loop does
// over MIGRATIONS_DIRS. They are deliberately NOT merged into one filesystem:
// module migration versions are unique within a directory but NOT across the
// repo (internal/account and internal/charging both ship a 20260720000001), and
// a merged FS would fail on the collision that the per-directory sequence
// handles fine. Order therefore matters only where one module's schema depends
// on another's; today none do (there are no cross-module foreign keys —
// ai/architecture.md §2), so any order works.
//
// Every other behaviour — the TEST_DATABASE_URL-then-container policy, ErrUnavailable,
// Result ownership — is identical to Provision.
func ProvisionDirs(ctx context.Context, migrationDirs ...string) (Result, error) {
	if len(migrationDirs) == 0 {
		return Result{}, fmt.Errorf("testdb: ProvisionDirs needs at least one migration directory")
	}
	// Fail fast and specifically: a mistyped relative path would otherwise
	// surface as an empty migration set and a mystifying "relation does not
	// exist" much later, inside a test.
	for _, dir := range migrationDirs {
		info, err := os.Stat(dir)
		if err != nil {
			return Result{}, fmt.Errorf("testdb: migration dir %q (paths are relative to the calling package's directory): %w", dir, err)
		}
		if !info.IsDir() {
			return Result{}, fmt.Errorf("testdb: migration path %q is not a directory", dir)
		}
	}

	return provision(ctx, func(ctx context.Context, dsn string) error {
		for _, dir := range migrationDirs {
			if err := applyMigrations(ctx, dsn, os.DirFS(dir)); err != nil {
				return fmt.Errorf("migration dir %s: %w", dir, err)
			}
		}
		return nil
	})
}

// provision holds the TEST_DATABASE_URL-then-testcontainer policy shared by Provision
// and ProvisionDirs. apply receives the DSN and is responsible for putting the
// caller's schema on it; it is retried, because Postgres may reset connections
// briefly after reporting ready.
func provision(ctx context.Context, apply func(context.Context, string) error) (Result, error) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn != "" {
		if err := retry(5, 2*time.Second, func() error { return apply(ctx, dsn) }); err != nil {
			log.Printf("testdb: TEST_DATABASE_URL not usable (%v); provisioning testcontainer", err)
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
			// The ONLY failure mode wrapped in ErrUnavailable: there is no Docker
			// daemon to start a container with. Everything below this point means
			// something is actually broken, and stays a plain (fatal) error.
			return Result{}, fmt.Errorf("%w: start postgres container: %w", ErrUnavailable, err)
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
		if err := retry(5, 2*time.Second, func() error { return apply(ctx, dsn) }); err != nil {
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

	// WithAllowOutofOrder is the Provider API's equivalent of the goose CLI's
	// -allow-missing, which the Makefile's migrate-up already passes for the same
	// reason: every module applies its own directory against ONE shared
	// goose_db_version table, so a directory's versions are routinely lower than
	// versions another module already recorded. Without this, applying
	// telemetry's dir to a database that already carries analytics' 20260821*
	// rows is refused as out of order.
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrationsFS,
		goose.WithAllowOutofOrder(true))
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
