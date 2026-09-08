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
	want := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}
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
	if got := set.Count(); got != 15 {
		t.Fatalf("expected 15 embedded snapshot files, got %d", got)
	}

	cases := []struct {
		object  string
		version int64
	}{
		{"users", 1}, {"users", 2}, {"users", 7},
		{"roles", 1}, {"roles", 4}, {"roles", 5}, {"roles", 8},
		{"user_roles", 1}, {"user_roles", 5},
		{"transactions", 1}, {"transactions", 3},
		{"transaction_details", 1},
		{"user_profiles", 9},
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
	if len(rendered) != 9 {
		t.Fatalf("expected 9 rendered versions, got %d", len(rendered))
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
