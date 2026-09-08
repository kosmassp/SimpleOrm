package migrations_test

import (
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// The diff command's own orchestration (§9, ADR-0017): new-table FK
// ordering, declared renames, --allow-remove, and that every emitted file
// parses as Go. The pure Diff/Emit* logic already has its own tests
// (migration_generator_test.go); this file mirrors the command-level cases
// of GeneratorTests.cs that the conformance runner (amend-cases) doesn't cover.

// diffBumpRoot is an empty, versioned root used only to give a DiffOptions.Set
// a "latest" version to compute the target from, without touching any entity.
type diffBumpRoot struct{ version int64 }

func (r diffBumpRoot) Version() int64                   { return r.version }
func (diffBumpRoot) Compose(*migrations.VersionBuilder) {}

type DiffParent struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column"`
}

func (DiffParent) Entity() core.EntityDef { return core.EntityDef{Source: core.Table("diff_parents")} }

type DiffChild struct {
	ID       int64 `orm:"column,key,generated"`
	ParentID int64 `orm:"column"`
}

func (DiffChild) Entity() core.EntityDef {
	return core.EntityDef{
		Source:      core.Table("diff_children"),
		ForeignKeys: []core.ForeignKeyDef{core.ForeignKey[DiffParent]("ParentID")},
	}
}

func TestExecuteDiff_NewTablesOrderFKReferencedFirst(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	emptySet, err := migrations.NewSet()
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := migrations.ExecuteDiff(ctx, migrations.DiffOptions{
		Set: emptySet,
		// Deliberately Child before Parent: alphabetical order alone would keep
		// this order; only FK-aware topological sort corrects it.
		EntityTypes:  []reflect.Type{reflect.TypeFor[DiffChild](), reflect.TypeFor[DiffParent]()},
		OutDir:       dir,
		Package:      "example.com/migrations",
		Dialect:      sqlite.New(),
		DialectLabel: "sqlite",
	}, &stdout, &stderr)

	if exit != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", exit, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}

	rootPath := filepath.Join(dir, "V0001.go")
	rootSource := readFile(t, rootPath)
	mustParseGo(t, rootPath, rootSource)
	if i, j := strings.Index(rootSource, "diffparent.V0001_Auto{}"), strings.Index(rootSource, "diffchild.V0001_Auto{}"); i == -1 || j == -1 || i > j {
		t.Fatalf("expected DiffParent composed before DiffChild in the root:\n%s", rootSource)
	}

	parentPath := filepath.Join(dir, "Table", "DiffParent", "V0001_Auto.go")
	childPath := filepath.Join(dir, "Table", "DiffChild", "V0001_Auto.go")
	mustParseGo(t, parentPath, readFile(t, parentPath))
	mustParseGo(t, childPath, readFile(t, childPath))
}

type DiffRenameWidget struct {
	ID   int64  `orm:"column,key,generated"`
	Note string `orm:"column"`
}

func (DiffRenameWidget) Entity() core.EntityDef {
	return core.EntityDef{Source: core.Table("diff_rename_widgets")}
}

func TestExecuteDiff_DeclaredRenameIsARenameNotAddPlusRemove(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	schema := &migrations.TableSchema{Name: "diff_rename_widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
		{Name: "remark", StorageType: "TEXT", Nullable: false},
	}}
	writeSnapshotFile(t, filepath.Join(dir, "Table", "DiffRenameWidget"), "V0001.schema.json",
		migrations.ExportSchema(schema, 1, time.Now()))

	set, err := migrations.NewSet(diffBumpRoot{version: 1})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := migrations.ExecuteDiff(ctx, migrations.DiffOptions{
		Set:          set,
		EntityTypes:  []reflect.Type{reflect.TypeFor[DiffRenameWidget]()},
		OutDir:       dir,
		Package:      "example.com/migrations",
		Dialect:      sqlite.New(),
		DialectLabel: "sqlite",
		Renames:      map[string]map[string]string{"diff_rename_widgets": {"remark": "note"}},
	}, &stdout, &stderr)

	if exit != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", exit, stderr.String())
	}
	stepPath := filepath.Join(dir, "Table", "DiffRenameWidget", "V0002_Auto.go")
	source := readFile(t, stepPath)
	mustParseGo(t, stepPath, source)
	if !strings.Contains(source, `actions.RenameColumn("remark", "note")`) {
		t.Fatalf("expected a declared rename, not add+remove:\n%s", source)
	}
	if strings.Contains(source, "AddColumn") || strings.Contains(source, "RemoveColumn") {
		t.Fatalf("a declared rename must not also read as add+remove:\n%s", source)
	}
}

type DiffRemovalWidget struct {
	ID int64 `orm:"column,key,generated"`
}

func (DiffRemovalWidget) Entity() core.EntityDef {
	return core.EntityDef{Source: core.Table("diff_removal_widgets")}
}

func TestExecuteDiff_RemovalsGateOnAllowRemove(t *testing.T) {
	ctx := context.Background()
	schema := &migrations.TableSchema{Name: "diff_removal_widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
		{Name: "legacy", StorageType: "TEXT", Nullable: true},
	}}

	newOptions := func(dir string, allowRemove bool) migrations.DiffOptions {
		set, err := migrations.NewSet(diffBumpRoot{version: 1})
		if err != nil {
			t.Fatal(err)
		}
		return migrations.DiffOptions{
			Set:          set,
			EntityTypes:  []reflect.Type{reflect.TypeFor[DiffRemovalWidget]()},
			OutDir:       dir,
			Package:      "example.com/migrations",
			Dialect:      sqlite.New(),
			DialectLabel: "sqlite",
			AllowRemove:  allowRemove,
		}
	}

	t.Run("refuses without --allow-remove", func(t *testing.T) {
		dir := t.TempDir()
		writeSnapshotFile(t, filepath.Join(dir, "Table", "DiffRemovalWidget"), "V0001.schema.json",
			migrations.ExportSchema(schema, 1, time.Now()))

		var stdout, stderr bytes.Buffer
		exit := migrations.ExecuteDiff(ctx, newOptions(dir, false), &stdout, &stderr)
		if exit != 1 {
			t.Fatalf("expected exit 1, got %d", exit)
		}
		if !strings.Contains(stderr.String(), "DDL-003") {
			t.Fatalf("expected a DDL-003 refusal, got %q", stderr.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "V0002.go")); err == nil {
			t.Fatal("nothing should have been written on refusal")
		}
	})

	t.Run("emits the removal with --allow-remove", func(t *testing.T) {
		dir := t.TempDir()
		writeSnapshotFile(t, filepath.Join(dir, "Table", "DiffRemovalWidget"), "V0001.schema.json",
			migrations.ExportSchema(schema, 1, time.Now()))

		var stdout, stderr bytes.Buffer
		exit := migrations.ExecuteDiff(ctx, newOptions(dir, true), &stdout, &stderr)
		if exit != 0 {
			t.Fatalf("expected exit 0, got %d; stderr=%s", exit, stderr.String())
		}
		stepPath := filepath.Join(dir, "Table", "DiffRemovalWidget", "V0002_Auto.go")
		source := readFile(t, stepPath)
		mustParseGo(t, stepPath, source)
		if !strings.Contains(source, `actions.RemoveColumn("legacy")`) {
			t.Fatalf("expected the removal to be emitted:\n%s", source)
		}
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func mustParseGo(t *testing.T, path, source string) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), path, source, parser.AllErrors); err != nil {
		t.Fatalf("%s does not parse as Go: %v\n%s", path, err, source)
	}
}
