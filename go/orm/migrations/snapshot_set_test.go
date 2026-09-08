package migrations_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

func writeSchemaFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotsFromDirectory_IndexesTableAndDDLSnapshots(t *testing.T) {
	dir := testsupport.TempDir(t)
	generatedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	writeSchemaFile(t, dir, "Table/Widget/V0001.schema.json",
		migrations.ExportSchema(&migrations.TableSchema{
			Name: "widgets",
			Columns: []migrations.TableColumn{
				{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
			},
		}, 1, generatedAt))
	writeSchemaFile(t, dir, "Table/Widget/V0002.schema.json",
		migrations.ExportSchema(&migrations.TableSchema{
			Name: "widgets",
			Columns: []migrations.TableColumn{
				{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
				{Name: "note", StorageType: "TEXT", Nullable: true},
			},
		}, 2, generatedAt))
	writeSchemaFile(t, dir, "View/WidgetTotal/V0001.schema.json",
		migrations.ExportDDL("widget_totals", "view", "create view widget_totals as select 1", 1, generatedAt))
	// A non-snapshot file must be ignored.
	writeSchemaFile(t, dir, "Table/Widget/notes.txt", "ignore me")

	set, err := migrations.SnapshotsFromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if set.Count() != 3 {
		t.Fatalf("expected 3 entries, got %d", set.Count())
	}

	at1 := set.At("widgets", 1)
	if at1 == nil || at1.Table == nil || len(at1.Table.Columns) != 1 {
		t.Fatalf("At(widgets, 1): %+v", at1)
	}
	// Object names compare case-insensitively.
	atUpper := set.At("WIDGETS", 2)
	if atUpper == nil || len(atUpper.Table.Columns) != 2 {
		t.Fatalf("At(WIDGETS, 2): %+v", atUpper)
	}

	latest := set.LatestBefore("widgets", 2)
	if latest == nil || len(latest.Table.Columns) != 1 {
		t.Fatalf("LatestBefore(widgets, 2) should be V0001, got %+v", latest)
	}
	if set.LatestBefore("widgets", 1) != nil {
		t.Fatal("LatestBefore at the creating version should be nil")
	}

	view := set.At("widget_totals", 1)
	if view == nil || view.Table != nil || view.DDL != "create view widget_totals as select 1" {
		t.Fatalf("At(widget_totals, 1): %+v", view)
	}
}

func TestSnapshotsFromDirectory_MissingDirectoryIsEmpty(t *testing.T) {
	set, err := migrations.SnapshotsFromDirectory(filepath.Join(testsupport.TempDir(t), "does-not-exist"))
	if err != nil {
		t.Fatal(err)
	}
	if set.Count() != 0 {
		t.Fatalf("expected an empty set, got %d entries", set.Count())
	}
}

func TestSnapshotsFromFS_ReadsEmbeddableFS(t *testing.T) {
	generatedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	doc := migrations.ExportSchema(&migrations.TableSchema{
		Name:    "widgets",
		Columns: []migrations.TableColumn{{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true}},
	}, 1, generatedAt)

	fsys := fstest.MapFS{
		"Table/Widget/V0001.schema.json": &fstest.MapFile{Data: []byte(doc)},
		"Table/Widget/readme.md":         &fstest.MapFile{Data: []byte("not a snapshot")},
	}

	set, err := migrations.SnapshotsFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if set.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", set.Count())
	}
	if entry := set.At("widgets", 1); entry == nil || entry.Table == nil {
		t.Fatalf("At(widgets, 1): %+v", entry)
	}
}
