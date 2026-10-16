package orm_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// This file covers Level 3 M3's declared hooks (spec/hooks.md) beyond the conformance/hook-cases runner:
// the MAP-026 shapes, the export, the session clock (VAL-020, the wall-clock default), the versioned
// soft-delete rules (CRUD-012, CRUD-010), reads that never filter on the flag, and a failed flush undoing
// a soft delete and a stamp. The entities below are test-local — never in the sample.

// --- test-local entities -----------------------------------------------------

// hookDoc is a versioned soft-delete entity with both audit hooks.
type hookDoc struct {
	ID           int64      `orm:"column,key,generated"`
	Title        string     `orm:"column"`
	Archived     bool       `orm:"column,soft_delete"`
	Version      int64      `orm:"column,version"`
	CreatedAtUtc time.Time  `orm:"column=created_at,created_at"`
	UpdatedAtUtc *time.Time `orm:"column=updated_at,updated_at"`
}

func (hookDoc) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("hook_docs")} }

// hookOffsetStamp keeps its created-at and updated-at as datetimeoffset columns, nullable and not.
type hookOffsetStamp struct {
	ID        int64      `orm:"column,key,generated"`
	CreatedAt time.Time  `orm:"column,type=datetimeoffset,created_at"`
	UpdatedAt *time.Time `orm:"column,type=datetimeoffset,updated_at"`
}

type hookCreatedAtOnString struct {
	ID    int64  `orm:"column,key,generated"`
	Stamp string `orm:"column,created_at"`
}

type hookUpdatedAtOnDate struct {
	ID    int64      `orm:"column,key,generated"`
	Stamp *time.Time `orm:"column,type=date,updated_at"`
}

type hookOnKey struct {
	Moment time.Time `orm:"column,key,created_at"`
}

type hookOnVersion struct {
	ID      int64 `orm:"column,key,generated"`
	Version int64 `orm:"column,version,updated_at"`
}

type hookOnGenerated struct {
	ID   int64  `orm:"column,key,generated,soft_delete"`
	Name string `orm:"column"`
}

type hookTwoUpdatedAt struct {
	ID      int64      `orm:"column,key,generated"`
	First   *time.Time `orm:"column,updated_at"`
	Another *time.Time `orm:"column,updated_at"`
}

type hookTwoHooksOnOneField struct {
	ID    int64     `orm:"column,key,generated"`
	Stamp time.Time `orm:"column,created_at,updated_at"`
}

type hookSoftDeleteOnString struct {
	ID   int64  `orm:"column,key,generated"`
	Flag string `orm:"column,soft_delete"`
}

type hookSoftDeleteNullable struct {
	ID   int64 `orm:"column,key,generated"`
	Flag *bool `orm:"column,soft_delete"`
}

type hookWithoutColumn struct {
	ID    int64     `orm:"column,key,generated"`
	Stamp time.Time `orm:"created_at"`
}

type hookOnIgnored struct {
	ID    int64     `orm:"column,key,generated"`
	Stamp time.Time `orm:"ignore,updated_at"`
}

type hookUnknownToken struct {
	ID    int64     `orm:"column,key,generated"`
	Stamp time.Time `orm:"column,created_on"`
}

type hookOnView struct {
	ID     int64     `orm:"column,key"`
	Moment time.Time `orm:"column,created_at"`
}

func (hookOnView) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("hook_view", "select 1 as id, '2026-01-01T00:00:00Z' as moment")}
}

type hookOnStatement struct {
	ID    int64      `orm:"column"`
	Flag  bool       `orm:"column,soft_delete"`
	Stamp *time.Time `orm:"column"`
}

func (hookOnStatement) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Statement("select 1 as id, 0 as flag, null as stamp")}
}

type hookOnProcedure struct {
	ID    int64      `orm:"column"`
	Stamp *time.Time `orm:"column,updated_at"`
}

func (hookOnProcedure) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Procedure("hook_procedure", "select 1 as id, null as stamp")}
}

type hookStampedAddress struct {
	City string `orm:"column,created_at"`
}

func (hookStampedAddress) OwnedType() {}

type hookOnOwnedMember struct {
	ID      int64               `orm:"column,key,generated"`
	Address *hookStampedAddress `orm:"owned"`
}

// --- helpers -----------------------------------------------------------------

func openHookDb(t *testing.T, options orm.Options) *orm.Db {
	t.Helper()
	db := testsupport.OpenSampleWith(t, options)
	createHookTable(t, db)
	return db
}

func createHookTable(t *testing.T, db *orm.Db) {
	t.Helper()
	if err := orm.CreateTable[hookDoc](context.Background(), db); err != nil {
		t.Fatalf("create hook_docs: %v", err)
	}
}

func insertHookDoc(t *testing.T, ctx context.Context, db *orm.Db, title string) *hookDoc {
	t.Helper()
	doc := &hookDoc{Title: title}
	if err := orm.Insert(ctx, db, doc); err != nil {
		t.Fatalf("insert hook doc: %v", err)
	}
	return doc
}

func sameInstant(got *time.Time, want time.Time) bool { return got != nil && got.Equal(want) }

// --- MAP-026 and the export ----------------------------------------------------

func TestHooks_InvalidDeclarationsAreMAP026(t *testing.T) {
	cases := map[string]error{
		"created_at on a string":                     loadMap[hookCreatedAtOnString](t),
		"updated_at on a date column":                loadMap[hookUpdatedAtOnDate](t),
		"a hook on a key":                            loadMap[hookOnKey](t),
		"a hook on the version column":               loadMap[hookOnVersion](t),
		"a hook on a generated column":               loadMap[hookOnGenerated](t),
		"two updated_at properties":                  loadMap[hookTwoUpdatedAt](t),
		"two hooks on one field":                     loadMap[hookTwoHooksOnOneField](t),
		"soft_delete on a non-bool":                  loadMap[hookSoftDeleteOnString](t),
		"soft_delete on a nullable bool":             loadMap[hookSoftDeleteNullable](t),
		"a hook without column":                      loadMap[hookWithoutColumn](t),
		"a hook on an ignored field":                 loadMap[hookOnIgnored](t),
		"a hook on an owned type's member":           loadMap[hookOnOwnedMember](t),
		"a hook on the owned navigation itself":      loadMap[hookOnOwnedNavigation](t),
		"a hook on a navigation (not a column)":      loadMap[hookOnNavigation](t),
		"a soft_delete next to a created_at (field)": loadMap[hookSoftDeleteAndCreatedAt](t),
		"a hook on a view-backed entity":             loadMap[hookOnView](t),
		"a hook on a statement-backed entity":        loadMap[hookOnStatement](t),
		"a hook on a procedure-backed entity":        loadMap[hookOnProcedure](t),
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			assertOnlyMAP026(t, err)
		})
	}
}

// assertOnlyMAP026 pins that a misplaced hook is MAP-026 and never also MAP-019/MAP-024 or another code
// (spec/hooks.md Clarifications: MAP-026 covers every misplaced hook).
func assertOnlyMAP026(t *testing.T, err error) {
	t.Helper()
	var mapping *core.MappingErrors
	if !errors.As(err, &mapping) || len(mapping.Errors) == 0 {
		t.Fatalf("expected a MappingErrors carrying MAP-026, got %v", err)
	}
	for _, e := range mapping.Errors {
		if e.Code != "MAP-026" {
			t.Errorf("expected only MAP-026, also got %v", e)
		}
	}
}

// The manual builder declares hooks under the same rules (spec/hooks.md Clarifications): every shape that can
// be wrong on a builder-declared table is MAP-026 there too.
func TestHooks_TheManualBuilderValidatesHooksLikeTheTag(t *testing.T) {
	type built struct {
		ID      int64
		Version int64
		Stamp   time.Time
		Other   time.Time
		Name    string
		Flag    bool
		Maybe   *bool
	}
	keyed := func(b *metadata.EntityMapBuilder[built]) { b.Property("ID").Key() }
	cases := map[string]func(b *metadata.EntityMapBuilder[built]){
		"a hook on the key":            func(b *metadata.EntityMapBuilder[built]) { b.Property("ID").Key().CreatedAt() },
		"a hook on the version":        func(b *metadata.EntityMapBuilder[built]) { b.Property("Version").Version().UpdatedAt() },
		"a hook on a generated column": func(b *metadata.EntityMapBuilder[built]) { b.Property("Flag").Generated().SoftDelete() },
		"created_at on a string":       func(b *metadata.EntityMapBuilder[built]) { b.Property("Name").CreatedAt() },
		"soft_delete on a nullable":    func(b *metadata.EntityMapBuilder[built]) { b.Property("Maybe").SoftDelete() },
		"two hooks on one property":    func(b *metadata.EntityMapBuilder[built]) { b.Property("Stamp").CreatedAt().UpdatedAt() },
		"one role on two properties": func(b *metadata.EntityMapBuilder[built]) {
			b.Property("Stamp").UpdatedAt()
			b.Property("Other").UpdatedAt()
		},
	}
	for name, declare := range cases {
		t.Run(name, func(t *testing.T) {
			builder := metadata.NewEntityMapBuilder[built]().ToTable("built")
			if name != "a hook on the key" {
				keyed(builder)
			}
			declare(builder)
			_, err := builder.Build(core.SnakeCaseConvention)
			assertOnlyMAP026(t, err)
		})
	}
}

type hookOnOwnedNavigation struct {
	ID      int64           `orm:"column,key,generated"`
	Address *sample.Address `orm:"owned,created_at"`
}

type hookOnNavigation struct {
	ID      int64        `orm:"column,key,generated"`
	OwnerID int64        `orm:"column"`
	Owner   *sample.User `orm:"many_to_one=OwnerID,updated_at"`
}

type hookSoftDeleteAndCreatedAt struct {
	ID    int64     `orm:"column,key,generated"`
	Stamp time.Time `orm:"column,created_at,soft_delete"`
}

func TestHooks_AnUnknownHookTokenStaysMAP023(t *testing.T) {
	if err := loadMap[hookUnknownToken](t); !orm.HasCode(err, "MAP-023") {
		t.Fatalf("expected MAP-023, got %v", err)
	}
}

func TestHooks_ValidDeclarationsLoadIntoTheMap(t *testing.T) {
	loader := metadata.NewLoader(nil)

	doc, err := metadata.Load[hookDoc](loader)
	if err != nil {
		t.Fatal(err)
	}
	if doc.CreatedAtProperty == nil || doc.CreatedAtProperty.ColumnName != "created_at" ||
		doc.UpdatedAtProperty == nil || doc.UpdatedAtProperty.ColumnName != "updated_at" ||
		doc.SoftDeleteProperty == nil || doc.SoftDeleteProperty.ColumnName != "archived" {
		t.Fatalf("the three hook properties resolve: %+v %+v %+v", doc.CreatedAtProperty, doc.UpdatedAtProperty, doc.SoftDeleteProperty)
	}

	// datetimeoffset columns, nullable or not, are valid hook targets.
	if _, err := metadata.Load[hookOffsetStamp](loader); err != nil {
		t.Fatalf("datetimeoffset hook columns load: %v", err)
	}

	// The manual builder declares hooks as well (spec/hooks.md: hooks are metadata, whichever loader reads them).
	type built struct {
		ID      int64
		Stamp   time.Time
		Changed *time.Time
		Gone    bool
	}
	builder := metadata.NewEntityMapBuilder[built]().ToTable("built")
	builder.Property("ID").Key().Generated()
	builder.Property("Stamp").CreatedAt()
	builder.Property("Changed").UpdatedAt()
	builder.Property("Gone").SoftDelete()
	m, err := builder.Build(core.SnakeCaseConvention)
	if err != nil {
		t.Fatal(err)
	}
	if m.CreatedAtProperty == nil || m.UpdatedAtProperty == nil || m.SoftDeleteProperty == nil {
		t.Fatalf("the builder's hooks resolve: %+v", m)
	}
}

func TestHooks_ExportCarriesAuditAndSoftDeleteOnlyWhenDeclared(t *testing.T) {
	loader := metadata.NewLoader(nil)
	export := func(m *orm.EntityMap) string {
		text, err := orm.ExportEntityMap(m, loader)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}

	doc, err := metadata.Load[hookDoc](loader)
	if err != nil {
		t.Fatal(err)
	}
	text := export(doc)
	for _, want := range []string{`"audit": "created_at"`, `"audit": "updated_at"`, `"softDelete": true`} {
		if !strings.Contains(text, want) {
			t.Errorf("the export carries %s:\n%s", want, text)
		}
	}

	// A map whose columns declare nothing exports exactly as before the milestone.
	plain, err := metadata.Load[uowNode](loader)
	if err != nil {
		t.Fatal(err)
	}
	if text := export(plain); strings.Contains(text, "audit") || strings.Contains(text, "softDelete") {
		t.Errorf("an entity with no hooks exports none:\n%s", text)
	}
}

// --- the clock ------------------------------------------------------------------

func TestHooks_ANonUTCClockIsVAL020WhenAHookReadsIt(t *testing.T) {
	ctx := context.Background()
	plusOne := time.FixedZone("UTC+1", 3600)
	db := testsupport.OpenSampleWith(t, orm.Options{
		Clock: func() time.Time { return time.Date(2026, 8, 29, 1, 0, 0, 0, plusOne) },
	})

	// A created-at the caller set needs no clock, so the insert succeeds.
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	unset := &sample.User{Name: "Grace", Email: "grace@example.com"}
	if err := orm.Insert(ctx, db, unset); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("an unset created-at reads the clock: expected VAL-020, got %v", err)
	}

	ada.Name = "Ada Lovelace"
	if err := orm.Update(ctx, db, ada); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("the update stamp reads the clock: expected VAL-020, got %v", err)
	}
	if ada.UpdatedAtUtc != nil {
		t.Errorf("a refused write leaves the instance's stamp as it was, got %v", ada.UpdatedAtUtc)
	}
	if err := orm.UpdateOnly(ctx, db, ada, "Name"); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("UpdateOnly stamps too: expected VAL-020, got %v", err)
	}

	stored := mustGet[sample.User](t, ctx, db, ada.ID)
	if stored.Name != "Ada" || stored.UpdatedAtUtc != nil {
		t.Errorf("no statement ran: row is %+v", stored)
	}
}

func TestHooks_AUTCClockInAFixedZoneOfOffsetZeroIsAccepted(t *testing.T) {
	ctx := context.Background()
	zero := time.FixedZone("GMT", 0)
	db := testsupport.OpenSampleWith(t, orm.Options{
		Clock: func() time.Time { return time.Date(2026, 8, 29, 0, 0, 0, 0, zero) },
	})
	user := &sample.User{Name: "Ada", Email: "ada@example.com"}
	if err := orm.Insert(ctx, db, user); err != nil {
		t.Fatal(err)
	}
	if !user.CreatedAtUtc.Equal(testsupport.ClockNow) {
		t.Errorf("created_at = %v, want the clock", user.CreatedAtUtc)
	}
}

func TestHooks_TheDefaultClockIsTheWallClock(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := orm.CreateTable[sample.User](ctx, db); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UTC().Add(-time.Second)
	user := &sample.User{Name: "Ada", Email: "ada@example.com"}
	if err := orm.Insert(ctx, db, user); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().Add(time.Second)
	if user.CreatedAtUtc.Before(before) || user.CreatedAtUtc.After(after) || user.CreatedAtUtc.Location() != time.UTC {
		t.Errorf("created_at = %v, want the wall clock in UTC between %v and %v", user.CreatedAtUtc, before, after)
	}
}

// --- audit columns ----------------------------------------------------------------

func TestHooks_CreatedAtFillsAnUnsetOffsetColumnAndKeepsACallersValue(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	if err := orm.CreateTable[hookOffsetStamp](ctx, db); err != nil {
		t.Fatal(err)
	}

	unset := &hookOffsetStamp{}
	if err := orm.Insert(ctx, db, unset); err != nil {
		t.Fatal(err)
	}
	if !unset.CreatedAt.Equal(testsupport.ClockNow) || unset.UpdatedAt != nil {
		t.Errorf("created_at takes the clock, updated_at stays null on insert: %+v", unset)
	}

	custom := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	set := &hookOffsetStamp{CreatedAt: custom}
	if err := orm.Insert(ctx, db, set); err != nil {
		t.Fatal(err)
	}
	if !set.CreatedAt.Equal(custom) {
		t.Errorf("a caller's created_at is kept, got %v", set.CreatedAt)
	}

	set.UpdatedAt = nil
	if err := orm.Update(ctx, db, set); err != nil {
		t.Fatal(err)
	}
	if !sameInstant(set.UpdatedAt, testsupport.ClockNow) || !set.CreatedAt.Equal(custom) {
		t.Errorf("an update stamps updated_at and never touches created_at: %+v", set)
	}
}

func TestHooks_UpdateOnlyListingTheStampColumnStillWritesTheClockOnce(t *testing.T) {
	ctx := context.Background()
	dialect := testsupport.NewRecordingDialect()
	db := testsupport.OpenSampleWith(t, orm.Options{Dialect: dialect})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	listed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	ada.UpdatedAtUtc = &listed
	ada.Name = "Ada Lovelace"
	dialect.Reset()
	if err := orm.UpdateOnly(ctx, db, ada, "Name", "UpdatedAtUtc"); err != nil {
		t.Fatal(err)
	}
	updates := dialect.Updates()
	if len(updates) != 1 || strings.Join(updates[0].Properties, ",") != "Name,UpdatedAtUtc" {
		t.Fatalf("the stamp column is written once, after the caller's: %+v", updates)
	}
	if !sameInstant(ada.UpdatedAtUtc, testsupport.ClockNow) {
		t.Errorf("the clock wins over the caller's value, got %v", ada.UpdatedAtUtc)
	}
}

func TestHooks_ATrackedInstanceDirtyOnlyInTheStampColumnWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	ada := mustGet[sample.User](t, ctx, db, 1)

	manual := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	ada.UpdatedAtUtc = &manual
	if changes, err := orm.ChangesOf(db, ada); err != nil || len(changes) != 1 || changes[0] != "UpdatedAtUtc" {
		t.Fatalf("the stamp column is reported as a change: %v %v", changes, err)
	}
	if err := orm.Update(ctx, db, ada); err != nil {
		t.Fatal(err)
	}
	summary := mustFlush(t, ctx, db)
	assertSummary(t, summary, 0, 0, 0)
	if !sameInstant(ada.UpdatedAtUtc, manual) {
		t.Errorf("nothing was written, so nothing was stamped; got %v", ada.UpdatedAtUtc)
	}
}

// --- soft delete ---------------------------------------------------------------------

func TestSoftDelete_AVersionedEntityRefusesTheByKeyFormWithCRUD012(t *testing.T) {
	ctx := context.Background()
	db := openHookDb(t, orm.Options{})
	doc := insertHookDoc(t, ctx, db, "Spec")

	if err := orm.Delete[hookDoc](ctx, db, doc.ID); orm.CodeOf(err) != "CRUD-012" {
		t.Fatalf("expected CRUD-012, got %v", err)
	}
	stored := mustGet[hookDoc](t, ctx, db, doc.ID)
	if stored.Archived || stored.Version != 0 {
		t.Errorf("a refused delete wrote nothing: %+v", stored)
	}
}

func TestSoftDelete_TheEntityFormSetsTheFlagStampsBumpsTheVersionAndTheRowStillReads(t *testing.T) {
	ctx := context.Background()
	db := openHookDb(t, orm.Options{Tracking: orm.TrackingTracked})
	doc := insertHookDoc(t, ctx, db, "Spec")
	if !db.IsTracked(doc) {
		t.Fatal("an inserted instance is tracked")
	}

	if err := orm.DeleteEntity(ctx, db, doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Archived || doc.Version != 1 || !sameInstant(doc.UpdatedAtUtc, testsupport.ClockNow) {
		t.Errorf("the flag and the stamp are set on the instance and the version bumped: %+v", doc)
	}
	if db.IsTracked(doc) {
		t.Error("a deleted instance is forgotten")
	}

	// Reads never filter on the flag: the key read and the criteria query both see the row.
	stored := mustGet[hookDoc](t, ctx, db, doc.ID)
	if !stored.Archived || stored.Version != 1 || !sameInstant(stored.UpdatedAtUtc, testsupport.ClockNow) {
		t.Errorf("the row is flagged, stamped, and versioned: %+v", stored)
	}
	all, err := orm.From[hookDoc](db).List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("a criteria query returns the flagged row: %v %v", all, err)
	}
	live, err := orm.From[hookDoc](db).Where(orm.Eq("Archived", false)).List(ctx)
	if err != nil || len(live) != 0 {
		t.Fatalf("the caller's explicit criteria filters it: %v %v", live, err)
	}
}

func TestSoftDelete_AStaleEntityIsCRUD010AndTheInstanceIsPutBack(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{})
	createHookTable(t, db)
	other := testsupport.OpenSampleAt(t, path, orm.Options{})
	doc := insertHookDoc(t, ctx, db, "Spec")

	elsewhere := mustGet[hookDoc](t, ctx, other, doc.ID)
	elsewhere.Title = "Edited elsewhere"
	if err := orm.Update(ctx, other, elsewhere); err != nil {
		t.Fatal(err)
	}

	err := orm.DeleteEntity(ctx, db, doc)
	if orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010, got %v", err)
	}
	if doc.Archived || doc.Version != 0 || doc.UpdatedAtUtc != nil {
		t.Errorf("the failed soft delete leaves the flag, version, and stamp as they were: %+v", doc)
	}
	if stored := mustGet[hookDoc](t, ctx, db, doc.ID); stored.Archived {
		t.Error("the row was not flagged")
	}
}

func TestSoftDelete_AMissingRowIsCRUD001InBothForms(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	if err := orm.Delete[sample.Role](ctx, db, int64(404)); orm.CodeOf(err) != "CRUD-001" {
		t.Errorf("by key: expected CRUD-001, got %v", err)
	}
	ghost := &sample.Role{ID: 404, Name: "ghost"}
	if err := orm.DeleteEntity(ctx, db, ghost); orm.CodeOf(err) != "CRUD-001" {
		t.Errorf("entity form: expected CRUD-001, got %v", err)
	}
	if ghost.IsDeleted || ghost.UpdatedAtUtc != nil {
		t.Errorf("a failed soft delete puts the instance back: %+v", ghost)
	}
}

func TestSoftDelete_ATrackedKeyIsForgottenByTheByKeyForm(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	role := insertRole(t, ctx, db, "auditor")

	if err := orm.Delete[sample.Role](ctx, db, role.ID); err != nil {
		t.Fatal(err)
	}
	if db.IsTracked(role) {
		t.Error("the by-key form forgets the key's instance like any deleted row")
	}
	if again := mustGet[sample.Role](t, ctx, db, role.ID); again == role || !again.IsDeleted {
		t.Errorf("a re-read is a fresh instance carrying the flag: %+v", again)
	}
}

// --- a failed flush undoes a stamp and a soft delete -----------------------------------

func TestFlush_AFailedFlushPutsBackTheStampOfAnUpdateAndTheFlagOfASoftDelete(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked})
	createHookTable(t, db)
	other := testsupport.OpenSampleAt(t, path, orm.Options{})

	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	doc := insertHookDoc(t, ctx, db, "Spec")

	// Another session edits the document: the tracked copy's version is stale.
	elsewhere := mustGet[hookDoc](t, ctx, other, doc.ID)
	elsewhere.Title = "Edited elsewhere"
	if err := orm.Update(ctx, other, elsewhere); err != nil {
		t.Fatal(err)
	}

	ada.Name = "Ada Lovelace"
	mustRemove(t, db, doc)
	if _, err := db.Flush(ctx); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010 on the stale soft delete, got %v", err)
	}

	if ada.UpdatedAtUtc != nil {
		t.Errorf("the stamp of the update that ran before the failure is put back, got %v", ada.UpdatedAtUtc)
	}
	if changes, err := orm.ChangesOf(db, ada); err != nil || len(changes) != 1 || changes[0] != "Name" {
		t.Errorf("the user is dirty again exactly as before: %v %v", changes, err)
	}
	if doc.Archived || doc.Version != 0 || doc.UpdatedAtUtc != nil || !db.IsTracked(doc) {
		t.Errorf("the soft-deleted instance is restored — flag, version, stamp, and tracking: %+v", doc)
	}
	if !db.HasPendingWork() {
		t.Error("the removal stays scheduled")
	}

	// Refresh is the way back; then everything flushes: the update, then the soft delete.
	if err := orm.Refresh(ctx, db, doc); err != nil {
		t.Fatal(err)
	}
	assertSummary(t, mustFlush(t, ctx, db), 0, 1, 1)
	if !doc.Archived || doc.Version != 2 || !sameInstant(doc.UpdatedAtUtc, testsupport.ClockNow) {
		t.Errorf("the retried soft delete lands: %+v", doc)
	}
	if !sameInstant(ada.UpdatedAtUtc, testsupport.ClockNow) {
		t.Errorf("the retried update stamps again, got %v", ada.UpdatedAtUtc)
	}
}

func TestFlush_AFailedFlushLeavesAnInsertsCreatedAtUnsetSoARetryFillsItAgain(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked})
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	linus := &sample.User{Name: "Linus", Email: "linus@example.com"}
	impostor := &sample.User{Name: "Impostor", Email: "ada@example.com"}
	mustPersist(t, db, linus)
	mustPersist(t, db, impostor)
	if _, err := db.Flush(ctx); err == nil {
		t.Fatal("the unique index on users.email must refuse the second insert")
	}
	if !linus.CreatedAtUtc.IsZero() {
		t.Errorf("the created_at the failed attempt filled is put back, got %v", linus.CreatedAtUtc)
	}

	mustRemove(t, db, impostor)
	assertSummary(t, mustFlush(t, ctx, db), 1, 0, 0)
	if !linus.CreatedAtUtc.Equal(testsupport.ClockNow) {
		t.Errorf("the retry fills created_at, got %v", linus.CreatedAtUtc)
	}
}

func TestHooks_ATrackedDirtyUpdateWritesTheDirtyColumnsInMetadataOrderThenTheStamp(t *testing.T) {
	ctx := context.Background()
	dialect := testsupport.NewRecordingDialect()
	db := testsupport.OpenSampleWith(t, orm.Options{Dialect: dialect, Tracking: orm.TrackingTracked})
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	ada := mustGet[sample.User](t, ctx, db, 1)

	// Changed in reverse of metadata order (own columns first, the inherited audit columns after).
	earlier := testsupport.SeedTime.Add(-time.Hour)
	ada.CreatedAtUtc = earlier
	ada.Email = "ada@new.example.com"
	ada.Name = "Ada Lovelace"
	dialect.Reset()
	if err := orm.Update(ctx, db, ada); err != nil {
		t.Fatal(err)
	}
	updates := dialect.Updates()
	if len(updates) != 1 || strings.Join(updates[0].Properties, ",") != "Name,Email,CreatedAtUtc,UpdatedAtUtc" {
		t.Fatalf("dirty columns in metadata order, then the stamp: %+v", updates)
	}
	if changes, err := orm.ChangesOf(db, ada); err != nil || len(changes) != 0 {
		t.Errorf("the stamp the update wrote is part of the new snapshot: %v %v", changes, err)
	}
}

func TestSoftDelete_CRUD012OutranksAMalformedKeyOnAVersionedByKeyDelete(t *testing.T) {
	ctx := context.Background()
	db := openHookDb(t, orm.Options{})
	insertHookDoc(t, ctx, db, "Spec")

	if err := orm.Delete[hookDoc](ctx, db, "not an integer key"); orm.CodeOf(err) != "CRUD-012" {
		t.Errorf("expected CRUD-012 before the key validates, got %v", err)
	}
	if err := orm.Delete[hookDoc](ctx, db, []any{int64(1), int64(2)}); orm.CodeOf(err) != "CRUD-012" {
		t.Errorf("expected CRUD-012 for a wrong-arity key too, got %v", err)
	}
}

type hookNamedTwice struct {
	ID    int64     `orm:"column,key,generated"`
	Stamp time.Time `orm:"column,created_at,created_at"`
}

func TestHooks_NamingTheSameHookTwiceOnATagIsAMalformedDeclarationMAP023(t *testing.T) {
	if err := loadMap[hookNamedTwice](t); !orm.HasCode(err, "MAP-023") {
		t.Fatalf("expected MAP-023, got %v", err)
	}
}
