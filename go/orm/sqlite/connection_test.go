package sqlite_test

import (
	"context"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// The driver seam end to end on a real temp-file database: open, BEGIN
// IMMEDIATE through the run lock, DDL inside it, and a statement description
// with origin metadata — the capability SchemaGuard builds on.
func TestDialect_OpensImmediateTransactionsAndDescribesStatements(t *testing.T) {
	ctx := context.Background()
	dialect := sqlite.New()
	pool, err := dialect.CreateConnection("Data Source=" + testsupport.TempDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	tx, err := dialect.BeginMigrationRunLock(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "create table widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, note TEXT) STRICT"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	described, err := dialect.DescribeStatement(ctx, conn, "select id, name as label, note, count(*) as n from widgets")
	if err != nil {
		t.Fatal(err)
	}
	if len(described) != 4 {
		t.Fatalf("described %d columns", len(described))
	}
	if described[0].Table != "widgets" || described[0].OriginColumn != "id" || described[0].DeclaredType != "INTEGER" {
		t.Errorf("origin metadata for id: %+v", described[0])
	}
	if described[1].Name != "label" || described[1].OriginColumn != "name" || described[1].DeclaredType != "TEXT" {
		t.Errorf("an alias keeps its origin: %+v", described[1])
	}
	if described[3].Name != "n" || described[3].Table != "" || described[3].DeclaredType != "" {
		t.Errorf("an expression column has no origin: %+v", described[3])
	}
	if _, err := dialect.DescribeStatement(ctx, conn, "select nope from nowhere"); err == nil {
		t.Error("a statement that fails to prepare reports an error")
	}
}

func TestDataSourceName_AcceptsPathsAndConnectionStrings(t *testing.T) {
	if got := sqlite.DataSourceName(`Data Source=C:\db\app.db`); got != `C:\db\app.db?_txlock=immediate` {
		t.Errorf("connection string form: %q", got)
	}
	if got := sqlite.DataSourceName("app.db"); got != "app.db?_txlock=immediate" {
		t.Errorf("bare path: %q", got)
	}
	if got := sqlite.DataSourceName("file:app.db?mode=rwc"); got != "file:app.db?mode=rwc&_txlock=immediate" {
		t.Errorf("file URL keeps its query: %q", got)
	}
}
