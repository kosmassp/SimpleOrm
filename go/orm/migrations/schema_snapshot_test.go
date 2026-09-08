package migrations_test

import (
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

func TestFromMap_BuildsStorageTypedSchema(t *testing.T) {
	schema := migrations.FromMap(widgetMap(), sqlite.New())
	if schema.Name != "widgets" {
		t.Fatalf("unexpected name: %q", schema.Name)
	}
	if len(schema.Columns) != 2 {
		t.Fatalf("unexpected columns: %+v", schema.Columns)
	}
}

func TestExportSchema_MatchesPinnedShape(t *testing.T) {
	schema := &migrations.TableSchema{
		Name: "roles",
		Columns: []migrations.TableColumn{
			{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
			{Name: "created_at", StorageType: "TEXT", Nullable: false},
			{Name: "name", StorageType: "TEXT", Nullable: false},
		},
		Indexes: []migrations.TableIndex{},
	}
	generatedAt := time.Date(2026, 8, 29, 11, 3, 2, 965629800, time.UTC)
	got := migrations.ExportSchema(schema, 1, generatedAt)
	want := `{
  "object": "roles",
  "asOfVersion": 1,
  "generatedAt": "2026-08-29T11:03:02.9656298Z",
  "columns": [
    {
      "column": "created_at",
      "type": "TEXT",
      "nullable": false
    },
    {
      "column": "id",
      "type": "INTEGER",
      "nullable": false,
      "key": true,
      "generated": true
    },
    {
      "column": "name",
      "type": "TEXT",
      "nullable": false
    }
  ],
  "indexes": []
}`
	if got != want {
		t.Fatalf("export mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestExportSchema_IndexesNameSortedWithDirectionAndUnique(t *testing.T) {
	schema := &migrations.TableSchema{
		Name:    "transactions",
		Columns: []migrations.TableColumn{{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true}},
		Indexes: []migrations.TableIndex{
			{Name: "ix_transactions_user_id", Columns: []migrations.IndexPart{{ColumnName: "user_id"}}},
			{
				Name: "ix_transactions_status_created",
				Columns: []migrations.IndexPart{
					{ColumnName: "status"}, {ColumnName: "created_at", Descending: true},
				},
			},
		},
	}
	got := migrations.ExportSchema(schema, 1, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	want := `{
  "object": "transactions",
  "asOfVersion": 1,
  "generatedAt": "2026-01-01T00:00:00.0000000Z",
  "columns": [
    {
      "column": "id",
      "type": "INTEGER",
      "nullable": false,
      "key": true,
      "generated": true
    }
  ],
  "indexes": [
    {
      "name": "ix_transactions_status_created",
      "columns": [
        {
          "column": "status",
          "direction": "asc"
        },
        {
          "column": "created_at",
          "direction": "desc"
        }
      ]
    },
    {
      "name": "ix_transactions_user_id",
      "columns": [
        {
          "column": "user_id",
          "direction": "asc"
        }
      ]
    }
  ]
}`
	if got != want {
		t.Fatalf("export mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestParseTable_RoundTripsExportSchema(t *testing.T) {
	schema := &migrations.TableSchema{
		Name: "widgets",
		Columns: []migrations.TableColumn{
			{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
			{Name: "name", StorageType: "TEXT", Nullable: false},
		},
		Indexes: []migrations.TableIndex{
			{Name: "ix_widgets_name", Columns: []migrations.IndexPart{{ColumnName: "name"}}, Unique: true},
		},
	}
	generatedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	exported := migrations.ExportSchema(schema, 9, generatedAt)

	parsed, asOfVersion, err := migrations.ParseTable([]byte(exported))
	if err != nil {
		t.Fatal(err)
	}
	if asOfVersion != 9 || parsed.Name != "widgets" || len(parsed.Columns) != 2 || len(parsed.Indexes) != 1 {
		t.Fatalf("round trip mismatch: %+v (asOfVersion=%d)", parsed, asOfVersion)
	}
	if !parsed.Indexes[0].Unique || parsed.Indexes[0].Columns[0].ColumnName != "name" {
		t.Fatalf("unexpected index: %+v", parsed.Indexes[0])
	}
}

func TestExportDDL_AndParseDDL_RoundTrip(t *testing.T) {
	generatedAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	exported := migrations.ExportDDL(
		"user_transaction_totals", "view", "create view   user_transaction_totals   as select 1", 6, generatedAt)
	want := `{
  "object": "user_transaction_totals",
  "kind": "view",
  "asOfVersion": 6,
  "generatedAt": "2026-08-29T12:00:00.0000000Z",
  "ddl": "create view user_transaction_totals as select 1"
}`
	if exported != want {
		t.Fatalf("export mismatch:\n--- got ---\n%s\n--- want ---\n%s", exported, want)
	}

	object, kind, ddl, asOfVersion, err := migrations.ParseDDL([]byte(exported))
	if err != nil {
		t.Fatal(err)
	}
	if object != "user_transaction_totals" || kind != "view" || asOfVersion != 6 ||
		ddl != "create view user_transaction_totals as select 1" {
		t.Fatalf("unexpected parse: object=%q kind=%q ddl=%q asOfVersion=%d", object, kind, ddl, asOfVersion)
	}
}

func TestNormalizeDDL_Vectors(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"collapses whitespace", "select   1\nfrom   t", "select 1 from t"},
		{
			"canonicalizes CREATE VIEW IF NOT EXISTS recasing",
			"CREATE VIEW IF NOT EXISTS Widgets AS select 1",
			"create view Widgets as select 1",
		},
		{
			"canonicalizes materialized view prefix",
			"CREATE   MATERIALIZED VIEW mv AS\nselect 1",
			"create materialized view mv as select 1",
		},
		{"non-view DDL only collapses whitespace", "alter  table t add column c TEXT", "alter table t add column c TEXT"},
		{
			"real fixture view text",
			"create view user_transaction_totals as select u.id as user_id from users u",
			"create view user_transaction_totals as select u.id as user_id from users u",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := migrations.NormalizeDDL(c.in); got != c.want {
				t.Errorf("NormalizeDDL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
