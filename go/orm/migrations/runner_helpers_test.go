package migrations_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// openMigrationsPool opens a fresh temp-file SQLite pool for the runner/diff
// tests in this package (ADR-0003: no mocks, no in-memory stand-ins).
func openMigrationsPool(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := sqlite.New().CreateConnection("Data Source=" + testsupport.TempDatabase(t))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// rawTableVersion builds a one-step, data-driven root version (migrations.SQLVersion) —
// the conformance-style shape used where the runner tests need a table without
// declaring a Go entity type.
func rawTableVersion(version int64, object, description string, up []string, down []string) *migrations.SQLVersion {
	return migrations.NewSQLVersion(version, migrations.SQLVersionStep{
		ObjectName: object, Description: description, Up: up, Down: down,
	})
}

// writeSnapshotFile writes one *.schema.json file under dir, for
// migrations.SnapshotsFromDirectory to read back.
func writeSnapshotFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir snapshot dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o666); err != nil {
		t.Fatalf("write snapshot file: %v", err)
	}
}
