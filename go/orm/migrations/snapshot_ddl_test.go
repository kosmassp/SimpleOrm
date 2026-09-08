package migrations_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

func TestCreateTableSQL_GeneratedKey(t *testing.T) {
	schema := &migrations.TableSchema{
		Name: "users",
		Columns: []migrations.TableColumn{
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
	if got := migrations.CreateTableSQL(schema); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateTableSQL_CompositeNaturalKey(t *testing.T) {
	// Columns render in the schema's own order (not sorted); the primary key
	// clause lists only the key columns, in that same order.
	schema := &migrations.TableSchema{
		Name: "user_roles",
		Columns: []migrations.TableColumn{
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
	if got := migrations.CreateTableSQL(schema); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateIndexSQL_UniqueAndDescending(t *testing.T) {
	index := migrations.TableIndex{
		Name: "ix_transactions_status_created",
		Columns: []migrations.IndexPart{
			{ColumnName: "status"},
			{ColumnName: "created_at", Descending: true},
		},
	}
	want := "create index if not exists ix_transactions_status_created on transactions (status, created_at desc)"
	if got := migrations.CreateIndexSQL("transactions", index); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	unique := migrations.TableIndex{Name: "ix_users_email", Columns: []migrations.IndexPart{{ColumnName: "email"}}, Unique: true}
	wantUnique := "create unique index if not exists ix_users_email on users (email)"
	if got := migrations.CreateIndexSQL("users", unique); got != wantUnique {
		t.Fatalf("got %q, want %q", got, wantUnique)
	}
}

func TestCreateIndexSQLs_RendersEveryIndex(t *testing.T) {
	schema := &migrations.TableSchema{
		Name: "users",
		Indexes: []migrations.TableIndex{
			{Name: "ix_users_email", Columns: []migrations.IndexPart{{ColumnName: "email"}}, Unique: true},
			{Name: "ix_users_display_name", Columns: []migrations.IndexPart{{ColumnName: "display_name"}}},
		},
	}
	got := migrations.CreateIndexSQLs(schema)
	if len(got) != 2 {
		t.Fatalf("expected 2 statements, got %v", got)
	}
	if got[0] != "create unique index if not exists ix_users_email on users (email)" {
		t.Errorf("unexpected first: %q", got[0])
	}
	if got[1] != "create index if not exists ix_users_display_name on users (display_name)" {
		t.Errorf("unexpected second: %q", got[1])
	}
}
