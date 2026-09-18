// Tests for the one-time baseline stamp. Everything else in this command is
// wiring that the compose deploy exercises directly.
//
// These are DB-backed: the stamp's whole behaviour lives in two SQL statements,
// and their guards (a schema that already holds tables, a ledger that is still
// empty) cannot be checked by reading strings. They follow the same
// TEST_DATABASE_URL-then-container policy as the module packages, and skip
// rather than fail when there is no Postgres and no Docker.
//
// Every test builds its OWN throw-away schema rather than reusing the migrated
// "account" one. The stamp's guards read the database, not the files, so a bare
// schema plus one table is a faithful pre-squash database — and giving each test
// its own means none of them can depend on the order the others ran in.
//
// Production impact: NONE. A _test.go file is never compiled into the shipped
// binary, so testcontainers and goose are not in the deploy image.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/cristianpena/magus-tesla-api/internal/config"
	"github.com/cristianpena/magus-tesla-api/internal/testdb"
)

// accountDir is a real migrations directory, reached from cmd/migrate. The tests
// use it only so BaselineVersion has files to read; which module it belongs to
// does not matter to them.
const accountDir = "../../internal/account/db/migrations"

var testDSN string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()

	result, err := testdb.ProvisionDirs(ctx,
		config.MigrationDir{Module: "account", Dir: accountDir},
	)
	switch {
	case errors.Is(err, testdb.ErrUnavailable):
		// No reachable Postgres and no Docker daemon. Skip rather than kill the
		// binary; newTestDB turns the empty testDSN into a t.Skip.
		log.Printf("migrate testdb: no Postgres available, SKIPPING DB-backed tests: %v", err)
		return m.Run()
	case err != nil:
		// A migration that failed to apply means the schema is genuinely broken.
		// Fail loudly — skipping would hide it.
		log.Fatalf("migrate testdb: provision: %v", err)
	}
	defer func() { _ = result.Terminate(context.Background()) }()

	testDSN = result.DSN
	return m.Run()
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	if testDSN == "" {
		t.Skip("no test database available")
	}
	db, err := sql.Open("pgx", testDSN)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newSchema creates an empty schema named after the test and drops it afterwards.
// withTable adds one ordinary table, which is what makes the schema look like a
// database built before the squash.
func newSchema(t *testing.T, db *sql.DB, name string, withTable bool) config.MigrationDir {
	t.Helper()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+name+` CASCADE`); err != nil {
		t.Fatalf("drop schema %s: %v", name, err)
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA `+name); err != nil {
		t.Fatalf("create schema %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+name+` CASCADE`)
	})

	if withTable {
		if _, err := db.ExecContext(ctx, `CREATE TABLE `+name+`.already_here (id integer)`); err != nil {
			t.Fatalf("create table in %s: %v", name, err)
		}
	}
	return config.MigrationDir{Module: name, Dir: accountDir}
}

// addGooseMarkerLedger reproduces what goose leaves behind when it creates a ledger
// and the migration that follows then fails: the table exists and holds only the
// version-0 marker row.
func addGooseMarkerLedger(t *testing.T, db *sql.DB, schema string) {
	t.Helper()
	ctx := context.Background()

	create := config.MigrationDir{Module: schema, Dir: accountDir}.StampBaselineSQL(1)[0]
	if _, err := db.ExecContext(ctx, create); err != nil {
		t.Fatalf("create ledger in %s: %v", schema, err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO "`+schema+`".goose_db_version (version_id, is_applied) VALUES (0, true)`); err != nil {
		t.Fatalf("insert goose marker in %s: %v", schema, err)
	}
}

func ledgerRows(t *testing.T, db *sql.DB, schema string) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT version_id FROM "` + schema + `".goose_db_version ORDER BY version_id`)
	if err != nil {
		t.Fatalf("read %s ledger: %v", schema, err)
	}
	defer func() { _ = rows.Close() }()

	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan %s ledger: %v", schema, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s ledger: %v", schema, err)
	}
	return out
}

// The pre-squash shape, which is what dev and prod look like: the module's tables
// are already there, but it has no ledger. stampBaseline must record the baseline
// as applied, so goose skips it instead of failing on "relation already exists".
func TestStampBaseline_PreSquashDatabaseIsStamped(t *testing.T) {
	db := newTestDB(t)
	m := newSchema(t, db, "stamp_presquash", true)

	if err := stampBaseline(context.Background(), db, m); err != nil {
		t.Fatalf("stampBaseline() returned error: %v", err)
	}

	want, err := m.BaselineVersion()
	if err != nil {
		t.Fatalf("BaselineVersion() returned error: %v", err)
	}
	got := ledgerRows(t, db, m.Module)
	if len(got) != 2 || got[0] != 0 || got[1] != want {
		t.Fatalf("ledger = %v, want [0 %d]", got, want)
	}
}

// A first deploy that already failed: goose created the ledger, wrote its version-0
// marker, then the baseline died on "relation already exists" and left the marker
// behind. That database still needs stamping, so a "ledger is empty" guard would be
// wrong — it would refuse, and the baseline would fail again on every retry. This is
// the state magus_test was found in.
func TestStampBaseline_LedgerWithOnlyGooseMarkerIsStamped(t *testing.T) {
	db := newTestDB(t)
	m := newSchema(t, db, "stamp_marker_only", true)
	addGooseMarkerLedger(t, db, m.Module)

	if err := stampBaseline(context.Background(), db, m); err != nil {
		t.Fatalf("stampBaseline() returned error: %v", err)
	}

	want, err := m.BaselineVersion()
	if err != nil {
		t.Fatalf("BaselineVersion() returned error: %v", err)
	}
	// The marker must not be duplicated: exactly 0 and the baseline.
	got := ledgerRows(t, db, m.Module)
	if len(got) != 2 || got[0] != 0 || got[1] != want {
		t.Fatalf("ledger = %v, want [0 %d]", got, want)
	}
}

// Running it repeatedly must change nothing. The deploy runs this on every start,
// so a second stamp would add duplicate rows forever.
func TestStampBaseline_IsIdempotent(t *testing.T) {
	db := newTestDB(t)
	m := newSchema(t, db, "stamp_repeat", true)

	for i := range 3 {
		if err := stampBaseline(context.Background(), db, m); err != nil {
			t.Fatalf("stampBaseline() run %d returned error: %v", i+1, err)
		}
	}

	if got := ledgerRows(t, db, m.Module); len(got) != 2 {
		t.Fatalf("ledger after 3 runs = %v, want exactly 2 rows", got)
	}
}

// A FRESH database: the schema exists (the runner creates it one line earlier) but
// holds no tables. Nothing may be recorded — otherwise goose would skip a baseline
// that never ran, and the database would end up with no tables at all. This is the
// case that makes running the stamp unconditionally safe, and the reason its guard
// tests for TABLES rather than for the schema.
func TestStampBaseline_FreshSchemaIsNotStamped(t *testing.T) {
	db := newTestDB(t)
	m := newSchema(t, db, "stamp_fresh", false)

	if err := stampBaseline(context.Background(), db, m); err != nil {
		t.Fatalf("stampBaseline() returned error: %v", err)
	}

	if got := ledgerRows(t, db, m.Module); len(got) != 0 {
		t.Fatalf("ledger on a fresh schema = %v, want no rows", got)
	}
}

// A database already past the squash: goose migrated "account" when this package's
// TestMain provisioned it, so its ledger holds real rows. The stamp must add
// nothing, or every deploy would append another version-0 row.
func TestStampBaseline_AlreadyMigratedDatabaseIsUntouched(t *testing.T) {
	db := newTestDB(t)
	m := config.MigrationDir{Module: "account", Dir: accountDir}

	before := ledgerRows(t, db, "account")
	if len(before) == 0 {
		t.Fatal("account ledger is empty; TestMain should have migrated it")
	}

	if err := stampBaseline(context.Background(), db, m); err != nil {
		t.Fatalf("stampBaseline() returned error: %v", err)
	}

	after := ledgerRows(t, db, "account")
	if len(after) != len(before) {
		t.Fatalf("ledger = %v, want it unchanged at %v", after, before)
	}
}
