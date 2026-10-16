package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/cli"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// Draft migrations at the command line (Level 3 M5, spec/migrations.md "Draft
// migrations"): `diff --draft` writes the IsDraft marker, migrate refuses a
// draft with MIG-013, and status lists it as Draft.

func run(t *testing.T, registry orm.Registry, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = cli.Run(context.Background(), registry, args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

func TestRun_DiffDraftWritesARootCarryingTheDraftMarker(t *testing.T) {
	empty, err := orm.NewMigrationSet()
	if err != nil {
		t.Fatal(err)
	}
	registry := orm.Registry{Entities: []reflect.Type{orm.TypeOf[sample.Role]()}, Migrations: empty}

	for _, draft := range []bool{false, true} {
		dir := t.TempDir()
		args := []string{"diff", "--out", dir, "--package", "example.com/migrations"}
		if draft {
			args = append(args, "--draft")
		}
		_, stderr, code := run(t, registry, args...)
		if code != 0 {
			t.Fatalf("diff (draft=%v): code=%d stderr=%s", draft, code, stderr)
		}
		root, err := os.ReadFile(filepath.Join(dir, "V0001.go"))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(root), "IsDraft() bool { return true }"); got != draft {
			t.Errorf("draft=%v: marker present = %v:\n%s", draft, got, root)
		}
	}
}

func TestRun_MigrateRefusesADraftAndStatusListsIt(t *testing.T) {
	set, err := orm.NewMigrationSet(
		orm.NewSQLVersion(1, orm.SQLVersionStep{
			ObjectName: "widgets", Description: "create",
			Up: []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"},
		}),
		orm.NewSQLVersion(2, orm.SQLVersionStep{
			ObjectName: "widgets", Description: "add_note",
			Up: []string{"alter table widgets add column note TEXT"},
		}).AsDraft(),
	)
	if err != nil {
		t.Fatal(err)
	}
	registry := orm.Registry{Migrations: set}
	dbPath := testsupport.TempDatabase(t)

	_, stderr, code := run(t, registry, "migrate", "--db", dbPath)
	if code == 0 || !strings.Contains(stderr, "MIG-013") {
		t.Fatalf("migrate: want a MIG-013 refusal, got code=%d stderr=%s", code, stderr)
	}

	out, stderr, code := run(t, registry, "status", "--db", dbPath)
	if code != 0 {
		t.Fatalf("status: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, "Draft") || !strings.Contains(out, "Pending") {
		t.Fatalf("status should list V0001 Pending and V0002 Draft, got:\n%s", out)
	}

	_, stderr, code = run(t, registry, "baseline", "--version", "2", "--db", dbPath)
	if code == 0 || !strings.Contains(stderr, "MIG-013") {
		t.Fatalf("baseline 2: want a MIG-013 refusal, got code=%d stderr=%s", code, stderr)
	}
	if _, stderr, code = run(t, registry, "baseline", "--version", "1", "--db", dbPath); code != 0 {
		t.Fatalf("baseline below the draft: code=%d stderr=%s", code, stderr)
	}
}
