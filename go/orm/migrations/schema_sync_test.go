package migrations_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// ADR-0017: force sync (migrate --force). Additive fixes are planned and
// applied; deletions are planned separately (gated by --allow-delete at the
// caller, DDL-003); type and nullability changes are never auto-applied
// (DDL-004). Mirrors SchemaSyncTests.cs.

type SyncNew struct {
	ID   int64
	Name string
}

func syncNewMap() *core.EntityMap {
	t := reflect.TypeFor[SyncNew]()
	return core.NewEntityMap(t, core.RelationTable, "sync_new_widgets", "", "", nil, []*core.PropertyMap{
		property(t, "ID", "id", core.TypeInt64, key, generated),
		property(t, "Name", "name", core.TypeString),
	}, core.KeyDatabaseGenerated, nil, nil)
}

type SyncAdd struct {
	ID   int64
	Name string
	Note *string
}

func syncAddMap() *core.EntityMap {
	t := reflect.TypeFor[SyncAdd]()
	note := property(t, "Note", "note", core.TypeString)
	note.IsNullable = true
	return core.NewEntityMap(t, core.RelationTable, "sync_add_widgets", "", "", nil, []*core.PropertyMap{
		property(t, "ID", "id", core.TypeInt64, key, generated),
		property(t, "Name", "name", core.TypeString),
		note,
	}, core.KeyDatabaseGenerated, nil, nil)
}

type SyncIndexed struct {
	ID   int64
	Name string
	Note *string
}

func syncIndexedMap() *core.EntityMap {
	t := reflect.TypeFor[SyncIndexed]()
	note := property(t, "Note", "note", core.TypeString)
	note.IsNullable = true
	m := core.NewEntityMap(t, core.RelationTable, "sync_idx_widgets", "", "", nil, []*core.PropertyMap{
		property(t, "ID", "id", core.TypeInt64, key, generated),
		property(t, "Name", "name", core.TypeString),
		note,
	}, core.KeyDatabaseGenerated, []*core.EntityIndex{
		{Name: "ix_sync_idx_widgets_name", Columns: []core.IndexColumn{{PropertyName: "Name", ColumnName: "name"}}},
	}, nil)
	return m
}

type SyncBad struct {
	ID   int64
	Note string
}

func syncBadMap() *core.EntityMap {
	t := reflect.TypeFor[SyncBad]()
	return core.NewEntityMap(t, core.RelationTable, "sync_bad_widgets", "", "", nil, []*core.PropertyMap{
		property(t, "ID", "id", core.TypeInt64, key, generated),
		property(t, "Note", "note", core.TypeString),
	}, core.KeyDatabaseGenerated, nil, nil)
}

func syncLoader() *metadata.Loader {
	options := (&metadata.Options{}).
		Register(explicitMap{entityType: reflect.TypeFor[SyncNew](), m: syncNewMap()}).
		Register(explicitMap{entityType: reflect.TypeFor[SyncAdd](), m: syncAddMap()}).
		Register(explicitMap{entityType: reflect.TypeFor[SyncIndexed](), m: syncIndexedMap()}).
		Register(explicitMap{entityType: reflect.TypeFor[SyncBad](), m: syncBadMap()})
	return metadata.NewLoader(options)
}

func TestSchemaSync_MissingTableIsCreatedAdditively(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	loader := syncLoader()

	plan, err := migrations.PlanSync(ctx, conn, sqlite.New(), loader, []reflect.Type{reflect.TypeFor[SyncNew]()})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sql := range plan.Additive {
		if strings.HasPrefix(sql, "create table if not exists sync_new_widgets") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an additive create table statement, got %v", plan.Additive)
	}
	if len(plan.Deletions) != 0 || len(plan.Unsupported) != 0 {
		t.Fatalf("expected no deletions/unsupported, got %+v", plan)
	}

	if err := migrations.ApplySync(ctx, conn, plan.Additive); err != nil {
		t.Fatal(err)
	}
	after, err := migrations.PlanSync(ctx, conn, sqlite.New(), loader, []reflect.Type{reflect.TypeFor[SyncNew]()})
	if err != nil {
		t.Fatal(err)
	}
	if !after.IsEmpty() {
		t.Fatalf("expected an empty plan after applying, got %+v", after)
	}
}

func TestSchemaSync_MissingNullableColumnIsAdditiveExtraIsDeletion(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	loader := syncLoader()

	if err := migrations.ApplySync(ctx, conn,
		[]string{"create table sync_add_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, junk INTEGER) STRICT"}); err != nil {
		t.Fatal(err)
	}

	plan, err := migrations.PlanSync(ctx, conn, sqlite.New(), loader, []reflect.Type{reflect.TypeFor[SyncAdd]()})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Additive) != 1 || plan.Additive[0] != "alter table sync_add_widgets add column note TEXT" {
		t.Fatalf("expected exactly the note column addition, got %v", plan.Additive)
	}
	if len(plan.Deletions) != 1 || plan.Deletions[0] != "alter table sync_add_widgets drop column junk" {
		t.Fatalf("expected exactly the junk column deletion, got %v", plan.Deletions)
	}
	if len(plan.Unsupported) != 0 {
		t.Fatalf("expected nothing unsupported, got %v", plan.Unsupported)
	}

	if err := migrations.ApplySync(ctx, conn, append(append([]string{}, plan.Additive...), plan.Deletions...)); err != nil {
		t.Fatal(err)
	}
	after, err := migrations.PlanSync(ctx, conn, sqlite.New(), loader, []reflect.Type{reflect.TypeFor[SyncAdd]()})
	if err != nil {
		t.Fatal(err)
	}
	if !after.IsEmpty() {
		t.Fatalf("expected an empty plan after applying, got %+v", after)
	}
}

func TestSchemaSync_IndexMatchingIsStructuralNotByName(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	loader := syncLoader()
	entities := []reflect.Type{reflect.TypeFor[SyncIndexed]()}

	// The model declares ix_sync_idx_widgets_name on (name); the DBA added the
	// same index under another name in an urgency: implemented.
	if err := migrations.ApplySync(ctx, conn, []string{
		"create table sync_idx_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, note TEXT) STRICT",
		"create index idx_dba_hotfix on sync_idx_widgets (name)",
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := migrations.PlanSync(ctx, conn, sqlite.New(), loader, entities)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.IsEmpty() {
		t.Fatalf("expected an equivalent index under another name to count as implemented, got %+v", plan)
	}

	// A structurally different index is not: the model's gets created, the stranger is a (gated) deletion.
	if err := migrations.ApplySync(ctx, conn, []string{
		"drop index idx_dba_hotfix", "create index idx_dba_hotfix on sync_idx_widgets (note)",
	}); err != nil {
		t.Fatal(err)
	}
	plan2, err := migrations.PlanSync(ctx, conn, sqlite.New(), loader, entities)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sql := range plan2.Additive {
		if strings.Contains(sql, "ix_sync_idx_widgets_name") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the model's index to be additive, got %v", plan2.Additive)
	}
	if len(plan2.Deletions) != 1 || plan2.Deletions[0] != "drop index idx_dba_hotfix" {
		t.Fatalf("expected the stranger index to be a deletion, got %v", plan2.Deletions)
	}
	if len(plan2.Unsupported) != 0 {
		t.Fatalf("expected nothing unsupported, got %v", plan2.Unsupported)
	}
}

func TestSchemaSync_TypeAndNullabilityChangesAreNeverAutoApplied(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	loader := syncLoader()

	if err := migrations.ApplySync(ctx, conn,
		[]string{"create table sync_bad_widgets (id INTEGER PRIMARY KEY, note INTEGER) STRICT"}); err != nil {
		t.Fatal(err)
	}
	plan, err := migrations.PlanSync(ctx, conn, sqlite.New(), loader, []reflect.Type{reflect.TypeFor[SyncBad]()})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Additive) != 0 || len(plan.Deletions) != 0 {
		t.Fatalf("expected no additive/deletion statements, got %+v", plan)
	}
	found := false
	for _, message := range plan.Unsupported {
		if strings.Contains(message, "sync_bad_widgets.note") && strings.Contains(message, "INTEGER") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an unsupported entry naming the type mismatch, got %v", plan.Unsupported)
	}
}
