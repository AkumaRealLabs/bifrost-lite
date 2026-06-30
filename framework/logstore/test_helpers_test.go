package logstore

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupPerfTestDB(t *testing.T) (*RDBLogStore, *gorm.DB) {
	t.Helper()

	store, err := newSqliteLogStore(context.Background(), &SQLiteConfig{
		Path: filepath.Join(t.TempDir(), "logs.db"),
	}, testLogger{})
	if err != nil {
		t.Fatalf("newSqliteLogStore() error = %v", err)
	}
	return store, store.db
}

func setupPostgresTestDB(t *testing.T) (*RDBLogStore, *gorm.DB) {
	t.Helper()

	db := trySetupPostgresDB(t)
	if db == nil {
		t.Skip("Postgres not available, skipping test")
	}

	resetPostgresLogstoreTestDB(t, db)
	if err := triggerMigrations(context.Background(), db, testLogger{}); err != nil {
		t.Fatalf("triggerMigrations() error = %v", err)
	}

	store := &RDBLogStore{db: db, logger: testLogger{}}
	t.Cleanup(func() {
		_ = store.Close(context.Background())
	})
	return store, db
}

func resetPostgresLogstoreTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Dialector.Name() != "postgres" {
		t.Fatalf("setupPostgresTestDB connected to %q, want postgres", db.Dialector.Name())
	}

	dropAllManagedMatViews(db)
	requireNoError(t, db.Exec("DROP TABLE IF EXISTS logs CASCADE").Error)
	requireNoError(t, db.Exec("DROP SEQUENCE IF EXISTS logs_inc_number_seq CASCADE").Error)
	requireNoError(t, db.Exec("DROP INDEX IF EXISTS idx_logs_metadata_gin").Error)
	requireNoError(t, db.Exec("CREATE TABLE IF NOT EXISTS migrations (id VARCHAR(255) PRIMARY KEY)").Error)
	requireNoError(t, db.Exec("DELETE FROM migrations").Error)
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func refreshTestMatViews(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Dialector.Name() != "postgres" {
		t.Skip("materialized views require postgres")
	}
	if err := refreshMatViews(context.Background(), db); err != nil {
		t.Fatalf("refreshMatViews() error = %v", err)
	}
}
