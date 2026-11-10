package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"census/src/reconcile"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

/*
TestMigrateAppliesScriptsOnce verifies fresh schema creation and repeated migration safety.

* @brief: TestMigrateAppliesScriptsOnce -> checks complete SQL scripts and filename-ledger replay
* @params: t [*testing.T] -> test reporter
*/
func TestMigrateAppliesScriptsOnce(t *testing.T) {
	ctx := context.Background()
	db := migrationTestDB(t)
	for range 2 {
		if err := db.Migrate(ctx, "../../migrations"); err != nil {
			t.Fatalf("Migrate(actual scripts): %v", err)
		}
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`, &count); err != nil || count != len(reconcile.CensusMigrations()) {
		t.Fatalf("Migrate(actual scripts) ledger count = %d, %v; want %d, nil", count, err, len(reconcile.CensusMigrations()))
	}
	dir := t.TempDir()
	body := `CREATE TABLE migration_probe (value TEXT);
		DO $body$ BEGIN INSERT INTO migration_probe VALUES ('first;value'); END $body$;`
	if err := os.WriteFile(filepath.Join(dir, "probe.sql"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.ApplyMigration(ctx, dir, "probe.sql"); err != nil {
			t.Fatalf("applyMigration(quoted script): %v", err)
		}
	}
	var value string
	if err := db.QueryRow(ctx, `SELECT count(*), min(value) FROM migration_probe`, &count, &value); err != nil || count != 1 || value != "first;value" {
		t.Fatalf("applyMigration(repeated quoted script) rows = %d, %q, %v; want 1, first;value, nil", count, value, err)
	}
}

/*
TestMigrationFailureRollsBack verifies a late SQL error undoes prior statements and leaves no ledger entry.

* @brief: TestMigrationFailureRollsBack -> checks failed and blank migration atomicity
* @params: t [*testing.T] -> test reporter
*/
func TestMigrationFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	db := migrationTestDB(t)
	if err := db.EnsureMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := `CREATE TABLE migration_probe (value TEXT);
		INSERT INTO migration_probe VALUES ('before failure');
		INSERT INTO missing_migration_table VALUES (1);`
	if err := os.WriteFile(filepath.Join(dir, "failure.sql"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigration(ctx, dir, "failure.sql"); err == nil {
		t.Fatal("applyMigration(late SQL error) = nil; want rollback error")
	}
	var relation *string
	if err := db.QueryRow(ctx, `SELECT to_regclass('migration_probe')::text`, &relation); err != nil || relation != nil {
		t.Fatalf("applyMigration(late SQL error) table = %v, %v; want nil, nil", relation, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blank.sql"), []byte(" \n\t"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigration(ctx, dir, "blank.sql"); err == nil {
		t.Fatal("applyMigration(blank file) = nil; want empty-file error")
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`, &count); err != nil || count != 0 {
		t.Fatalf("applyMigration(failed scripts) ledger count = %d, %v; want 0, nil", count, err)
	}
}

/*
migrationTestDB creates a disposable schema inside the explicitly configured test database.

* @brief: migrationTestDB -> isolates migration tests without touching the application's schema
* @params: t [*testing.T] -> test reporter and cleanup owner
* @returns: db [*DB] -> pool restricted to the disposable schema
*/
func migrationTestDB(t *testing.T) *reconcile.DB {
	t.Helper()
	databaseURL := os.Getenv("CENSUS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set CENSUS_TEST_DATABASE_URL to an isolated PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := reconcile.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	id, err := reconcile.CensusUUID()
	if err != nil {
		t.Fatal(err)
	}
	schema := "migration_test_" + strings.ReplaceAll(id, "-", "")
	if err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean migration test schema: %v", err)
		}
	})
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := reconcile.NewDB(stdlib.OpenDB(*config))
	t.Cleanup(func() { _ = db.Close() })
	return db
}
