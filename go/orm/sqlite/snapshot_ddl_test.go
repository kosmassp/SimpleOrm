package sqlite_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// The snapshot-shape renderings of the SQLite dialect (ADR-0043; they were the migrations package's SnapshotDdl
// before the seam took them): frozen, because the derived rollbacks and the shadow baseline restore through them.

func TestCreateTableSQL_GeneratedKey(t *testing.T) {
	schema := &core.TableSchema{
		Name: "users",
		Columns: []core.TableColumn{
			{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
			{Name: "name", StorageType: "TEXT", Nullable: false},
			{Name: "note", StorageType: "TEXT", Nullable: true},
		},
	}
	want := "create table if not exists users (\n" +
		"    id INTEGER PRIMARY KEY,\n" +
		"    name TEXT NOT NULL,\n" +
		"    note TEXT\n" +
		") STRICT"
	if got := sqlite.New().CreateTableFromSchemaSQL(schema); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateTableSQL_CompositeNaturalKey(t *testing.T) {
	// Columns render in the schema's own order (not sorted); the primary key
	// clause lists only the key columns, in that same order.
	schema := &core.TableSchema{
		Name: "user_roles",
		Columns: []core.TableColumn{
			{Name: "created_at", StorageType: "TEXT", Nullable: false},
			{Name: "role_id", StorageType: "INTEGER", Nullable: false, Key: true},
			{Name: "user_id", StorageType: "INTEGER", Nullable: false, Key: true},
		},
	}
	want := "create table if not exists user_roles (\n" +
		"    created_at TEXT NOT NULL,\n" +
		"    role_id INTEGER NOT NULL,\n" +
		"    user_id INTEGER NOT NULL,\n" +
		"    primary key (role_id, user_id)\n" +
		") STRICT"
	if got := sqlite.New().CreateTableFromSchemaSQL(schema); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateIndexSQL_UniqueAndDescending(t *testing.T) {
	index := core.TableIndex{
		Name: "ix_transactions_status_created",
		Columns: []core.IndexPart{
			{ColumnName: "status"},
			{ColumnName: "created_at", Descending: true},
		},
	}
	want := "create index if not exists ix_transactions_status_created on transactions (status, created_at desc)"
	if got := sqlite.New().CreateIndexFromSchemaSQL("transactions", index); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	unique := core.TableIndex{Name: "ix_users_email", Columns: []core.IndexPart{{ColumnName: "email"}}, Unique: true}
	wantUnique := "create unique index if not exists ix_users_email on users (email)"
	if got := sqlite.New().CreateIndexFromSchemaSQL("users", unique); got != wantUnique {
		t.Fatalf("got %q, want %q", got, wantUnique)
	}
}
