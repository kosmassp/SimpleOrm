package migrations_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// Draft migrations (Level 3 M5, spec/migrations.md "Draft migrations"): the
// model's renamed_from declaration read by the generator beside --rename, and
// `diff --draft` with the IsDraft marker the runner refuses (MIG-013).

// DraftWidget declares its note column renamed from remark.
type DraftWidget struct {
	ID   int64   `orm:"column,key,generated"`
	Note *string `orm:"column,renamed_from=remark"`
}

func (DraftWidget) Entity() core.EntityDef {
	return core.EntityDef{Source: core.Table("draft_widgets")}
}

func draftWidgetSnapshot(column string, version int64) string {
	return migrations.ExportSchema(&migrations.TableSchema{Name: "draft_widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: column, StorageType: "TEXT", Nullable: true},
	}}, version, time.Now())
}

func runDiff(t *testing.T, dir string, latest int64, mutate func(*migrations.DiffOptions)) (exit int, stdout, stderr string) {
	t.Helper()
	set, err := migrations.NewSet(diffBumpRoot{version: latest})
	if err != nil {
		t.Fatal(err)
	}
	options := migrations.DiffOptions{
		Set:          set,
		EntityTypes:  []reflect.Type{reflect.TypeFor[DraftWidget]()},
		OutDir:       dir,
		Package:      "example.com/migrations",
		Dialect:      sqlite.New(),
		DialectLabel: "sqlite",
	}
	if mutate != nil {
		mutate(&options)
	}
	var out, errBuf bytes.Buffer
	exit = migrations.ExecuteDiff(context.Background(), options, &out, &errBuf)
	return exit, out.String(), errBuf.String()
}

func TestDeclaredRename_AppliesOnceAgainstThePreRenameShape(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotFile(t, filepath.Join(dir, "Table", "DraftWidget"), "V0001.schema.json", draftWidgetSnapshot("remark", 1))

	exit, _, stderr := runDiff(t, dir, 1, nil)
	if exit != 0 {
		t.Fatalf("diff: exit %d, stderr=%s", exit, stderr)
	}
	step := readFile(t, filepath.Join(dir, "Table", "DraftWidget", "V0002_Auto.go"))
	if !strings.Contains(step, `actions.RenameColumn("remark", "note")`) {
		t.Fatalf("expected the declared rename as one RenameColumn:\n%s", step)
	}
	if strings.Contains(step, "AddColumn") || strings.Contains(step, "RemoveColumn") {
		t.Fatalf("a declared rename must not also read as add + remove:\n%s", step)
	}
}

func TestDeclaredRename_DeclaresNothingOnceTheSnapshotHasTheNewColumn(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotFile(t, filepath.Join(dir, "Table", "DraftWidget"), "V0002.schema.json", draftWidgetSnapshot("note", 2))

	exit, stdout, stderr := runDiff(t, dir, 2, nil)
	if exit != 0 || stderr != "" {
		t.Fatalf("diff: exit %d, stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "no schema changes") {
		t.Fatalf("expected no schema changes, got: %s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "V0003.go")); err == nil {
		t.Fatal("a version was written although the declaration was already applied")
	}
}

func TestDeclaredRename_AnExplicitRenameOfTheAppliedPairIsUnsupported(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotFile(t, filepath.Join(dir, "Table", "DraftWidget"), "V0002.schema.json", draftWidgetSnapshot("note", 2))

	exit, _, stderr := runDiff(t, dir, 2, func(o *migrations.DiffOptions) {
		o.Renames = map[string]map[string]string{"draft_widgets": {"remark": "note"}}
	})
	if exit == 0 || !strings.Contains(stderr, "DDL-004") {
		t.Fatalf("an explicit rename stays strict: exit %d, stderr=%s", exit, stderr)
	}
}

func TestDeclaredRename_AnExplicitRenameWinsOverTheDeclarationOfTheSameOldColumn(t *testing.T) {
	// remark is declared to become note; the explicit flag sends it to memo instead (and note is added).
	current := &migrations.TableSchema{Name: "w", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true},
		{Name: "memo", StorageType: "TEXT", Nullable: true},
		{Name: "note", StorageType: "TEXT", Nullable: true},
	}}
	snapshot := &migrations.TableSchema{Name: "w", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true},
		{Name: "remark", StorageType: "TEXT", Nullable: true},
	}}

	diff := migrations.DiffSchemas(current, snapshot,
		map[string]string{"remark": "memo"}, map[string]string{"remark": "note"})

	if len(diff.Renamed) != 1 || diff.Renamed[0].From != "remark" || diff.Renamed[0].To != "memo" {
		t.Fatalf("renamed = %v, want only remark -> memo", diff.Renamed)
	}
	if len(diff.Added) != 1 || diff.Added[0].Name != "note" {
		t.Fatalf("added = %v, want note", diff.Added)
	}
	if len(diff.Unsupported) != 0 {
		t.Fatalf("unsupported = %v, want none", diff.Unsupported)
	}
}

func TestDeclaredRenames_ReadsEachPropertyOnceInto_OldToNew(t *testing.T) {
	m, err := metadata.Load[DraftWidget](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	declared := migrations.DeclaredRenames(m)
	if len(declared) != 1 || declared["remark"] != "note" {
		t.Fatalf("declared = %v, want remark -> note", declared)
	}
}

func TestDeclaredRename_DeclaresNothingWhileTheModelStillMapsTheOldColumn(t *testing.T) {
	// note is declared renamed from remark, but the model keeps mapping remark: two columns, so note simply adds.
	current := &migrations.TableSchema{Name: "w", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true},
		{Name: "remark", StorageType: "TEXT", Nullable: true},
		{Name: "note", StorageType: "TEXT", Nullable: true},
	}}
	snapshot := &migrations.TableSchema{Name: "w", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true},
		{Name: "remark", StorageType: "TEXT", Nullable: true},
	}}

	diff := migrations.DiffSchemas(current, snapshot, nil, map[string]string{"remark": "note"})

	if len(diff.Renamed) != 0 || len(diff.Removed) != 0 || len(diff.Unsupported) != 0 {
		t.Fatalf("renamed=%v removed=%v unsupported=%v, want none of them", diff.Renamed, diff.Removed, diff.Unsupported)
	}
	if len(diff.Added) != 1 || diff.Added[0].Name != "note" {
		t.Fatalf("added = %v, want note", diff.Added)
	}
}

func TestDeclaredRename_AnExplicitRenameMatchesCaseInsensitivelyAndIsAppliedOnce(t *testing.T) {
	current := &migrations.TableSchema{Name: "w", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true},
		{Name: "note", StorageType: "TEXT", Nullable: true},
	}}
	snapshot := &migrations.TableSchema{Name: "w", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true},
		{Name: "remark", StorageType: "TEXT", Nullable: true},
	}}

	diff := migrations.DiffSchemas(current, snapshot, map[string]string{"REMARK": "note"}, map[string]string{"remark": "note"})

	if len(diff.Renamed) != 1 || len(diff.Unsupported) != 0 || len(diff.Added) != 0 {
		t.Fatalf("renamed=%v unsupported=%v added=%v, want one rename and nothing else", diff.Renamed, diff.Unsupported, diff.Added)
	}
}

func TestDiff_DraftEmitsTheMarkerOnTheRootOnlyWhenAsked(t *testing.T) {
	for _, draft := range []bool{false, true} {
		dir := t.TempDir()
		exit, _, stderr := runDiff(t, dir, 0, func(o *migrations.DiffOptions) { o.Draft = draft })
		if exit != 0 {
			t.Fatalf("diff (draft=%v): exit %d, stderr=%s", draft, exit, stderr)
		}
		rootPath := filepath.Join(dir, "V0001.go")
		source := readFile(t, rootPath)
		mustParseGo(t, rootPath, source)

		if got := strings.Contains(source, "IsDraft() bool { return true }"); got != draft {
			t.Errorf("draft=%v: marker present = %v:\n%s", draft, got, source)
		}
		if migrations.HasDraftMarker(source) != draft {
			t.Errorf("draft=%v: HasDraftMarker disagrees:\n%s", draft, source)
		}
		step := readFile(t, filepath.Join(dir, "Table", "DraftWidget", "V0001_Auto.go"))
		if strings.Contains(step, "IsDraft") {
			t.Errorf("draft=%v: the marker belongs on the root, not the step:\n%s", draft, step)
		}
	}
}

// --- the runner -------------------------------------------------------------

func draftPlan(t *testing.T, draftFrom int64) *migrations.Set {
	t.Helper()
	versions := []migrations.Version{
		rawTableVersion(1, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil),
	}
	second := rawTableVersion(2, "widgets", "add_note", []string{"alter table widgets add column note TEXT"}, nil)
	if draftFrom == 2 {
		second.AsDraft()
	}
	versions = append(versions, second)
	set, err := migrations.NewSet(versions...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestRunner_RefusesAPendingDraftOnMigrateBeforeAnythingApplies(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), draftPlan(t, 2), nil)

	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); core.CodeOf(err) != "MIG-013" {
		t.Fatalf("migrate: want MIG-013, got %v", err)
	}
	entries, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// V0001 stays unapplied too: the whole plan refuses.
	for _, e := range entries {
		want := migrations.Pending
		if e.Version == 2 {
			want = migrations.Draft
		}
		if e.State != want {
			t.Errorf("%s: state %v, want %v", e, e.State, want)
		}
	}
}

func TestRunner_StatusReportsADraftAsDraftAndStillCountsItPending(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), draftPlan(t, 2), nil)

	entries, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if entries[1].State != migrations.Draft || entries[1].State.String() != "Draft" || !strings.Contains(entries[1].String(), "Draft") {
		t.Fatalf("expected the second step to read Draft, got %v", entries)
	}
	if pending, _ := runner.HasPending(ctx); !pending {
		t.Error("an unapplied draft is still an unapplied version")
	}
}

func TestRunner_RefusesBaselineAtOrAboveAPendingDraftButNotBelow(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), draftPlan(t, 2), nil)

	if err := runner.Baseline(ctx, 2); core.CodeOf(err) != "MIG-013" {
		t.Fatalf("baseline 2: want MIG-013, got %v", err)
	}
	if err := runner.Baseline(ctx, 1); err != nil {
		t.Fatalf("a baseline below the draft is fine: %v", err)
	}
	entries, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].State != migrations.Applied || entries[1].State != migrations.Draft {
		t.Fatalf("after baseline 1: %v", entries)
	}
}

func TestRunner_AppliesTheSamePlanOnceTheMarkerIsRemoved(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	if _, err := migrations.NewRunner(pool, sqlite.New(), maps, draftPlan(t, 2), nil).Migrate(ctx, migrations.RunOptions{}); core.CodeOf(err) != "MIG-013" {
		t.Fatalf("want MIG-013, got %v", err)
	}
	applied, err := migrations.NewRunner(pool, sqlite.New(), maps, draftPlan(t, 0), nil).Migrate(ctx, migrations.RunOptions{})
	if err != nil || applied != 2 {
		t.Fatalf("reviewed plan: applied %d, err %v", applied, err)
	}
}

// V0099 is a hand-shaped typed root, the way the generator writes one: type-name version plus the IsDraft method.
type V0099 struct{}

func (V0099) Compose(version *migrations.VersionBuilder) {
	rawTableVersion(99, "gadgets", "create", []string{"create table gadgets (id INTEGER PRIMARY KEY) STRICT"}, nil).Compose(version)
}

func (V0099) IsDraft() bool { return true }

func TestRunner_ReadsTheGeneratedIsDraftMethodOfATypedRoot(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	set, err := migrations.NewSet(V0099{})
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), set, nil)

	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); core.CodeOf(err) != "MIG-013" {
		t.Fatalf("want MIG-013 from the IsDraft method, got %v", err)
	}
}

func TestRunner_AnAlreadyRecordedDraftIsJustAnAppliedVersion(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	if _, err := migrations.NewRunner(pool, sqlite.New(), maps, draftPlan(t, 0), nil).Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	// A draft whose version is already recorded is history, not pending work, so it is not refused (ADR-0038).
	runner := migrations.NewRunner(pool, sqlite.New(), maps, draftPlan(t, 2), nil)
	applied, err := runner.Migrate(ctx, migrations.RunOptions{})
	if err != nil || applied != 0 {
		t.Fatalf("applied %d, err %v", applied, err)
	}
	entries, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if entries[1].State != migrations.Applied {
		t.Errorf("a recorded draft keeps its normal state, got %v", entries[1].State)
	}
}

func TestShadow_ReplaysADraftLikeAnyVersion(t *testing.T) {
	result, err := sqlite.Shadow(context.Background(), sqlite.ShadowOptions{
		Set: draftPlan(t, 2), OutDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("shadow must replay a draft: %v", err)
	}
	if len(result.Notes) == 0 {
		t.Error("expected the replay notes for the objects without mapped entities")
	}
}

func TestRunner_TheRefusalNamesTheVersion(t *testing.T) {
	pool := openMigrationsPool(t)
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), draftPlan(t, 2), nil)

	_, err := runner.Migrate(context.Background(), migrations.RunOptions{})

	var coded *core.Error
	if !errors.As(err, &coded) || coded.Code != "MIG-013" || coded.Target != "V0002" {
		t.Fatalf("want MIG-013 naming V0002, got %v", err)
	}
}

func TestRunner_MigrateDownIgnoresTheMarker(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)
	plan := func(draft bool) *migrations.Set {
		second := rawTableVersion(2, "widgets", "add_note",
			[]string{"alter table widgets add column note TEXT"}, []string{"alter table widgets drop column note"})
		if draft {
			second.AsDraft()
		}
		set, err := migrations.NewSet(
			rawTableVersion(1, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil), second)
		if err != nil {
			t.Fatal(err)
		}
		return set
	}
	if _, err := migrations.NewRunner(pool, sqlite.New(), maps, plan(false), nil).Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	marked := migrations.NewRunner(pool, sqlite.New(), maps, plan(true), nil)
	reverted, err := marked.MigrateDown(ctx, 1, migrations.RunOptions{})
	if err != nil || reverted != 1 {
		t.Fatalf("down over a recorded draft: reverted %d, err %v", reverted, err)
	}
	// Rolled back, the version is pending again -- and now it is pending work the marker refuses.
	if _, err := marked.Migrate(ctx, migrations.RunOptions{}); core.CodeOf(err) != "MIG-013" {
		t.Fatalf("want MIG-013 for the rolled-back draft, got %v", err)
	}
}
