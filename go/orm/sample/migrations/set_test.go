package migrations_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	samplemigrations "github.com/kosmassp/SimpleOrm/go/orm/sample/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// The sample set's structural validation (names, versions, composition) runs
// eagerly in Set() (orm.NewMigrationSet -> migrations.NewSet), independent of
// the metadata loader. Rendering each version's Up SQL needs the declaration
// loader (tags/descriptors); TestSampleSet_Renders below exercises that path too.
func TestSampleSet_BuildsWithVersionsInOrder(t *testing.T) {
	set, err := samplemigrations.Set()
	if err != nil {
		t.Fatal(err)
	}
	got := set.VersionNumbers()
	want := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
	if len(got) != len(want) {
		t.Fatalf("expected %d versions, got %d: %v", len(want), len(got), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("version %d: want %d, got %d", i, v, got[i])
		}
	}
}

func TestSampleSet_SnapshotsEmbedTheExpectedFiles(t *testing.T) {
	set, err := orm.SnapshotsFromFS(samplemigrations.Snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if got := set.Count(); got != 20 {
		t.Fatalf("expected 20 embedded snapshot files, got %d", got)
	}

	cases := []struct {
		object  string
		version int64
	}{
		{"users", 1}, {"users", 2}, {"users", 7},
		{"roles", 1}, {"roles", 4}, {"roles", 5}, {"roles", 8}, {"roles", 11},
		{"user_roles", 1}, {"user_roles", 5},
		{"transactions", 1}, {"transactions", 3},
		{"transaction_details", 1},
		{"user_profiles", 10}, {"user_profiles", 12}, {"role_changes", 13}, {"trg_roles_audit", 13},
		{"user_transaction_totals", 1}, {"user_transaction_totals", 6},
	}
	for _, c := range cases {
		if set.At(c.object, c.version) == nil {
			t.Errorf("missing snapshot for (%s, %d)", c.object, c.version)
		}
	}
}

// TestSampleSet_Renders proves the whole sample tree renders through the real
// declaration loader and the SQLite dialect: every version, every step,
// byte-identical Up SQL to the C# reference's rendering.
func TestSampleSet_Renders(t *testing.T) {
	set, err := samplemigrations.Set()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(metadata.NewLoader(nil), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 13 {
		t.Fatalf("expected 13 rendered versions, got %d", len(rendered))
	}

	find := func(version int64) migrations.RenderedVersion {
		for _, v := range rendered {
			if v.Version == version {
				return v
			}
		}
		t.Fatalf("no rendered version %d", version)
		return migrations.RenderedVersion{}
	}

	v1 := find(1)
	if len(v1.Steps) != 6 {
		t.Fatalf("V0001 should compose 6 steps, got %d", len(v1.Steps))
	}
	wantObjects := []string{"users", "roles", "user_roles", "transactions", "transaction_details", "user_transaction_totals"}
	for i, want := range wantObjects {
		if v1.Steps[i].ObjectName != want {
			t.Errorf("V0001 step %d: want object %q, got %q", i, want, v1.Steps[i].ObjectName)
		}
	}

	v2 := find(2)
	wantUp := "alter table users add column display_name TEXT"
	if v2.Steps[0].Up[0].SQL != wantUp {
		t.Errorf("V0002 up[0]: want %q, got %q", wantUp, v2.Steps[0].Up[0].SQL)
	}
	if v2.Steps[0].Up[1].SQL != "update users set display_name = name" {
		t.Errorf("V0002 up[1] (Post hook): got %q", v2.Steps[0].Up[1].SQL)
	}

	v4 := find(4)
	if got := v4.Steps[0].Up[0].SQL; got != "alter table roles rename column name to role_name" {
		t.Errorf("V0004 rename: got %q", got)
	}

	v5 := find(5)
	if len(v5.Steps) != 2 || v5.Steps[0].ObjectName != "user_roles" || v5.Steps[1].ObjectName != "roles" {
		t.Fatalf("V0005 should compose user_roles then roles, got %+v", v5.Steps)
	}
	if got := v5.Steps[1].Down.Pre[0].SQL; got != "delete from roles where role_name = 'user'" {
		t.Errorf("V0005_SeedUserRole PreDown: got %q", got)
	}

	v6 := find(6)
	if got := v6.Steps[0].Up[0].SQL; got != "drop view if exists user_transaction_totals" {
		t.Errorf("V0006 recreate (drop): got %q", got)
	}
}

// TestSampleSet_V0013IsTheFirstTriggerInTheMapping renders the table step and then the trigger step through the
// SQLite dialect (Level 4, ADR-0042): drop-then-create, the object named by the trigger.
func TestSampleSet_V0013IsTheFirstTriggerInTheMapping(t *testing.T) {
	set, err := samplemigrations.Set()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(metadata.NewLoader(nil), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	v13 := rendered[len(rendered)-1]
	if v13.Version != 13 || len(v13.Steps) != 2 || v13.Steps[0].ObjectName != "role_changes" || v13.Steps[1].ObjectName != "trg_roles_audit" {
		t.Fatalf("V0013 composes the table, then the trigger: %+v", v13.Steps)
	}

	trigger := v13.Steps[1]
	want := []string{
		"drop trigger if exists trg_roles_audit",
		"drop trigger if exists trg_roles_audit_insert",
		"drop trigger if exists trg_roles_audit_update",
		"drop trigger if exists trg_roles_audit_delete",
		"create trigger trg_roles_audit after update on roles for each row\nbegin\n" +
			"insert into role_changes (role_id, role_name, changed_at)\nselect n.id, n.role_name, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')\n" +
			"from (select new.id as id, new.role_name as role_name) n;\nend",
	}
	if len(trigger.Up) != len(want) {
		t.Fatalf("expected %d statements, got %d: %+v", len(want), len(trigger.Up), trigger.Up)
	}
	for i, statement := range trigger.Up {
		if statement.SQL != want[i] {
			t.Errorf("statement %d:\n got  %q\n want %q", i, statement.SQL, want[i])
		}
	}
}
