package orm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// This file covers Level 3 M2 beyond the conformance/flush-cases runner
// (spec/unit-of-work.md): cascade declarations (MAP-025), cycles (UOW-004),
// a failed flush keeping its schedule and its session state, cascades on every
// shape the fixture does not carry, plan-first refusals, and immediate
// operations beside the unit of work. The entities below are test-local — never
// in the sample.

// --- test-local entities -----------------------------------------------------

// uowNode is a self-referencing entity (a tree): the cycle and chain fixtures.
type uowNode struct {
	ID       int64    `orm:"column,key,generated"`
	Name     string   `orm:"column"`
	ParentID *int64   `orm:"column"`
	Parent   *uowNode `orm:"many_to_one=ParentID"`
}

func (uowNode) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:      orm.Table("uow_nodes"),
		ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[uowNode]("ParentID")},
	}
}

// uowOwner owns one uowOwnerDetail through a cascading one-to-one.
type uowOwner struct {
	ID     int64           `orm:"column,key,generated"`
	Name   string          `orm:"column"`
	Detail *uowOwnerDetail `orm:"one_to_one=OwnerID,cascade=persist+remove"`
}

func (uowOwner) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("uow_owners")} }

type uowOwnerDetail struct {
	ID      int64  `orm:"column,key,generated"`
	OwnerID int64  `orm:"column"`
	Note    string `orm:"column"`
}

func (uowOwnerDetail) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("uow_owner_details")}
}

// uowPersistOnly cascades only Persist, from a many-to-one.
type uowPersistOnly struct {
	ID     int64    `orm:"column,key,generated"`
	NodeID int64    `orm:"column"`
	Node   *uowNode `orm:"many_to_one=NodeID,cascade=persist"`
}

func (uowPersistOnly) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("uow_persist_only"), ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[uowNode]("NodeID")}}
}

type uowBadManyToMany struct {
	ID    int64          `orm:"column,key,generated"`
	Roles []*sample.Role `orm:"many_to_many,cascade=persist"`
}

type uowBadColumnCascade struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column,cascade=remove"`
}

type uowBadIgnoreCascade struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"ignore,cascade=remove"`
}

type uowBadOwnedCascade struct {
	ID      int64           `orm:"column,key,generated"`
	Address *sample.Address `orm:"owned,cascade=persist"`
}

type uowBadCascadeToken struct {
	ID    int64      `orm:"column,key,generated"`
	Peers []*uowNode `orm:"one_to_many=ParentID,cascade=sometimes"`
}

// --- helpers -----------------------------------------------------------------

func openTrackedWithTables(t *testing.T) *orm.Db {
	t.Helper()
	db := openTracked(t)
	createTestTables(t, db)
	return db
}

func createTestTables(t *testing.T, db *orm.Db) {
	t.Helper()
	ctx := context.Background()
	for _, create := range []func(context.Context, *orm.Db) error{
		orm.CreateTable[uowNode],
		orm.CreateTable[uowOwner],
		orm.CreateTable[uowOwnerDetail],
		orm.CreateTable[uowPersistOnly],
	} {
		if err := create(ctx, db); err != nil {
			t.Fatalf("create test table: %v", err)
		}
	}
}

func newUser(name, email string) *sample.User {
	user := &sample.User{Name: name, Email: email}
	user.CreatedAtUtc = testsupport.SeedTime
	return user
}

func newDetail(transactionID int64, description string) *sample.TransactionDetail {
	detail := &sample.TransactionDetail{
		TransactionID: transactionID, Description: description, Quantity: 1, UnitPrice: orm.MustDecimal("2.50"),
	}
	detail.CreatedAtUtc = testsupport.SeedTime
	return detail
}

func mustFlush(t *testing.T, ctx context.Context, db *orm.Db) orm.FlushSummary {
	t.Helper()
	summary, err := db.Flush(ctx)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	return summary
}

func mustPersist[T any](t *testing.T, db *orm.Db, entity *T) {
	t.Helper()
	if err := orm.Persist(db, entity); err != nil {
		t.Fatalf("persist %T: %v", entity, err)
	}
}

func mustRemove[T any](t *testing.T, db *orm.Db, entity *T) {
	t.Helper()
	if err := orm.Remove(db, entity); err != nil {
		t.Fatalf("remove %T: %v", entity, err)
	}
}

func rowCount[T any](t *testing.T, db *orm.Db) int {
	t.Helper()
	rows, err := orm.QueryAll[T](context.Background(), db)
	if err != nil {
		t.Fatalf("select-all %T: %v", rows, err)
	}
	return len(rows)
}

func assertSummary(t *testing.T, got orm.FlushSummary, inserted, updated, deleted int) {
	t.Helper()
	if got != (orm.FlushSummary{Inserted: inserted, Updated: updated, Deleted: deleted}) {
		t.Fatalf("summary = %+v, want inserted %d updated %d deleted %d", got, inserted, updated, deleted)
	}
}

// --- MAP-025 and the export ---------------------------------------------------

func loadMap[T any](t *testing.T) error {
	t.Helper()
	_, err := metadata.Load[T](metadata.NewLoader(nil))
	return err
}

func TestCascade_DeclarationsThatAreNotANavigationAreMAP025(t *testing.T) {
	cases := map[string]error{
		"a many_to_many navigation": loadMap[uowBadManyToMany](t),
		"a plain column":            loadMap[uowBadColumnCascade](t),
		"an ignored field":          loadMap[uowBadIgnoreCascade](t),
		"an owned navigation":       loadMap[uowBadOwnedCascade](t),
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			if !orm.HasCode(err, "MAP-025") {
				t.Fatalf("expected MAP-025, got %v", err)
			}
		})
	}
}

func TestCascade_UnknownOperationIsMAP023(t *testing.T) {
	if err := loadMap[uowBadCascadeToken](t); !orm.HasCode(err, "MAP-023") {
		t.Fatalf("expected MAP-023 for an unknown cascade token, got %v", err)
	}
}

func TestCascade_ExportCarriesTheDeclaredOperationsOnly(t *testing.T) {
	loader := metadata.NewLoader(nil)
	export := func(m *orm.EntityMap) string {
		text, err := orm.ExportEntityMap(m, loader)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}

	transaction, err := metadata.Load[sample.Transaction](loader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(export(transaction), `"cascade": [`) {
		t.Error("Transaction.Details declares a cascade: the export carries it")
	}
	user, err := metadata.Load[sample.User](loader)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(export(user), "cascade") {
		t.Error("User declares no cascade: its export is unchanged")
	}

	persistOnly, err := metadata.Load[uowPersistOnly](loader)
	if err != nil {
		t.Fatal(err)
	}
	text := export(persistOnly)
	if !strings.Contains(text, `"persist"`) || strings.Contains(text, `"remove"`) {
		t.Errorf("a persist-only cascade exports [\"persist\"] alone:\n%s", text)
	}
	if got := persistOnly.Relationships[0].Cascade; got != orm.CascadePersist {
		t.Errorf("RelationshipMap.Cascade = %v, want persist", got)
	}
}

// --- the calls ---------------------------------------------------------------

func TestUnitOfWork_UntrackedSessionRefusesEveryCall(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	user := newUser("Ada", "ada@example.com")

	if err := orm.Persist(db, user); orm.CodeOf(err) != "UOW-003" {
		t.Errorf("Persist: expected UOW-003, got %v", err)
	}
	if err := orm.Remove(db, user); orm.CodeOf(err) != "UOW-003" {
		t.Errorf("Remove: expected UOW-003, got %v", err)
	}
	if _, err := db.Flush(ctx); orm.CodeOf(err) != "UOW-003" {
		t.Errorf("Flush: expected UOW-003, got %v", err)
	}
	if db.HasPendingWork() {
		t.Error("an untracked session never has pending work")
	}
}

func TestUnitOfWork_PersistOfATrackedInstanceSchedulesNothingButItsCascadesStillReachNewChildren(t *testing.T) {
	ctx := context.Background()
	db := openTrackedWithTables(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	transaction := insertJoinTransaction(t, ctx, db, ada.ID, "5")

	// A never-loaded collection of a tracked instance cascades nothing: no hidden load.
	mustPersist(t, db, transaction)
	if db.HasPendingWork() {
		t.Fatal("persisting a tracked instance with a nil collection schedules nothing")
	}

	// An initialized collection reaches its new member; the child takes the owner's key at flush.
	detail := newDetail(0, "Widget")
	transaction.Details = []*sample.TransactionDetail{detail}
	mustPersist(t, db, transaction)
	if !db.HasPendingWork() {
		t.Fatal("the new detail must be scheduled by the cascade")
	}
	assertSummary(t, mustFlush(t, ctx, db), 1, 0, 0)
	if detail.TransactionID != transaction.ID || !db.IsTracked(detail) {
		t.Errorf("the detail takes the owner's key (%d) and becomes tracked, got %d", transaction.ID, detail.TransactionID)
	}
}

func TestUnitOfWork_RemovingAScheduledParentCancelsTheInsertsItCascaded(t *testing.T) {
	ctx := context.Background()
	db := openTrackedWithTables(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	existing := insertJoinTransaction(t, ctx, db, ada.ID, "5")

	d1, d2 := newDetail(existing.ID, "Widget"), newDetail(0, "Gadget")
	parent := newTransaction(ada.ID)
	parent.Details = []*sample.TransactionDetail{d1, d2}

	mustPersist(t, db, d1) // d1 is also persisted directly: it stays when the parent goes
	mustPersist(t, db, parent)
	mustRemove(t, db, parent)

	if !db.HasPendingWork() {
		t.Fatal("the directly persisted detail must stay scheduled")
	}
	assertSummary(t, mustFlush(t, ctx, db), 1, 0, 0)
	if !db.IsTracked(d1) {
		t.Error("the directly persisted detail was inserted")
	}
	if db.IsTracked(d2) || db.IsTracked(parent) {
		t.Error("the parent and the child only its cascade had scheduled were cancelled")
	}
	if got := rowCount[sample.TransactionDetail](t, db); got != 1 {
		t.Errorf("one detail row expected, got %d", got)
	}
}

func TestUnitOfWork_OneToOneCascadePersistsAndRemovesTheSingleTarget(t *testing.T) {
	ctx := context.Background()
	db := openTrackedWithTables(t)

	owner := &uowOwner{Name: "o1", Detail: &uowOwnerDetail{Note: "n"}}
	bare := &uowOwner{Name: "o2"} // a nil singular navigation cascades nothing
	mustPersist(t, db, owner)
	mustPersist(t, db, bare)
	assertSummary(t, mustFlush(t, ctx, db), 3, 0, 0)
	if owner.Detail.OwnerID != owner.ID || !db.IsTracked(owner.Detail) {
		t.Fatalf("the detail takes the owner's generated key, got %d (owner %d)", owner.Detail.OwnerID, owner.ID)
	}

	mustRemove(t, db, bare)
	assertSummary(t, mustFlush(t, ctx, db), 0, 0, 1)
	if got := rowCount[uowOwnerDetail](t, db); got != 1 {
		t.Fatalf("removing the bare owner touches no detail, %d left", got)
	}

	mustRemove(t, db, owner)
	assertSummary(t, mustFlush(t, ctx, db), 0, 0, 2)
	if rowCount[uowOwner](t, db) != 0 || rowCount[uowOwnerDetail](t, db) != 0 {
		t.Error("the owner and its detail are both deleted")
	}
	if db.IsTracked(owner) || db.IsTracked(owner.Detail) {
		t.Error("deleted instances are forgotten")
	}
}

func TestUnitOfWork_ManyToOneCascadePersistsTheReferencedParentFirst(t *testing.T) {
	ctx := context.Background()
	db := openTrackedWithTables(t)

	node := &uowNode{Name: "n"}
	item := &uowPersistOnly{Node: node} // persisting the item persists the node it references
	mustPersist(t, db, item)
	assertSummary(t, mustFlush(t, ctx, db), 2, 0, 0)
	if item.NodeID != node.ID || node.ID == 0 || !db.IsTracked(node) {
		t.Errorf("the node inserts first and its key flows into the item (node %d, fk %d)", node.ID, item.NodeID)
	}
}

func TestUnitOfWork_ARefusalAnywhereInACascadeSchedulesNothing(t *testing.T) {
	ctx := context.Background()
	db := openTrackedWithTables(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	transaction := insertJoinTransaction(t, ctx, db, ada.ID, "5")
	known := newDetail(transaction.ID, "Widget")
	if err := orm.Insert(ctx, db, known); err != nil {
		t.Fatal(err)
	}

	t.Run("remove: an untracked member of a loaded collection is UOW-001", func(t *testing.T) {
		if err := orm.Load(ctx, db, transaction, "Details"); err != nil {
			t.Fatal(err)
		}
		transaction.Details = append(transaction.Details, newDetail(transaction.ID, "stranger"))
		if err := orm.Remove(db, transaction); orm.CodeOf(err) != "UOW-001" {
			t.Fatalf("expected UOW-001, got %v", err)
		}
		if db.HasPendingWork() {
			t.Error("the transaction and the tracked detail were planned, then dropped with the refusal")
		}
	})

	t.Run("persist: a member scheduled for removal is UOW-006", func(t *testing.T) {
		mustRemove(t, db, known)
		fresh := newTransaction(ada.ID)
		fresh.Details = []*sample.TransactionDetail{newDetail(0, "new"), known}
		if err := orm.Persist(db, fresh); orm.CodeOf(err) != "UOW-006" {
			t.Fatalf("expected UOW-006, got %v", err)
		}
		// Only the earlier removal is pending: the new transaction and its first detail were never scheduled.
		assertSummary(t, mustFlush(t, ctx, db), 0, 0, 1)
		if db.IsTracked(fresh) {
			t.Error("the refused persist scheduled nothing")
		}
	})
}

// --- order -------------------------------------------------------------------

func TestFlush_ACycleIsUOW004BeforeAnySQLAndTheSameChainOrdersParentFirst(t *testing.T) {
	ctx := context.Background()
	db := openTrackedWithTables(t)

	a, b := &uowNode{Name: "a"}, &uowNode{Name: "b"}
	a.Parent, b.Parent = b, a
	mustPersist(t, db, a)
	mustPersist(t, db, b)
	if _, err := db.Flush(ctx); orm.CodeOf(err) != "UOW-004" {
		t.Fatalf("expected UOW-004, got %v", err)
	}
	if !db.HasPendingWork() || rowCount[uowNode](t, db) != 0 {
		t.Error("a cycle is refused before any SQL and the schedule stays")
	}

	// The same two instances without the cycle: the child is persisted first, the parent still inserts first.
	b.Parent = nil
	assertSummary(t, mustFlush(t, ctx, db), 2, 0, 0)
	if a.ParentID == nil || *a.ParentID != b.ID || b.ID >= a.ID {
		t.Errorf("the parent inserts first and its key flows into the child (parent %d, child %d, fk %v)", b.ID, a.ID, a.ParentID)
	}
}

// --- failure -----------------------------------------------------------------

func TestFlush_AFailedFlushThatOwnsItsTransactionAppliesNothingAndKeepsTheSchedule(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked})
	other := testsupport.OpenSampleAt(t, path, orm.Options{})
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	good, duplicate := newUser("Linus", "linus@example.com"), newUser("Impostor", "ada@example.com")
	mustPersist(t, db, good)
	mustPersist(t, db, duplicate)

	if _, err := db.Flush(ctx); err == nil {
		t.Fatal("the unique index on users.email must refuse the second insert")
	}
	if got := rowCount[sample.User](t, other); got != 1 {
		t.Fatalf("the failed flush applied nothing, %d users in the database", got)
	}
	if !db.HasPendingWork() {
		t.Fatal("a failed flush keeps the schedule")
	}
	if db.IsTracked(good) || good.ID != 0 {
		t.Errorf("the insert that succeeded before the failure is undone in memory (tracked %v, id %d)", db.IsTracked(good), good.ID)
	}

	// Cancelling the bad insert lets the rest flush.
	mustRemove(t, db, duplicate)
	assertSummary(t, mustFlush(t, ctx, db), 1, 0, 0)
	if !db.IsTracked(good) || good.ID == 0 || rowCount[sample.User](t, other) != 2 {
		t.Error("the remaining insert flushes and tracks with its generated key")
	}
}

func TestFlush_AFailedFlushInsideTheCallersTransactionLeavesItOpen(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked})
	other := testsupport.OpenSampleAt(t, path, orm.Options{})
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustPersist(t, db, newUser("Linus", "linus@example.com"))
	mustPersist(t, db, newUser("Impostor", "ada@example.com"))
	if _, err := db.Flush(ctx); err == nil {
		t.Fatal("expected the unique violation")
	}
	if _, err := db.Begin(ctx); orm.CodeOf(err) != "TX-001" {
		t.Fatalf("the flush leaves the caller's transaction open, got %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rowCount[sample.User](t, other); got != 1 {
		t.Errorf("the caller's rollback undid the rows written before the failure, %d users left", got)
	}
	if !db.HasPendingWork() {
		t.Error("the schedule is kept")
	}
}

func TestFlush_AFailedUpdateRestoresEarlierSnapshotsAndInsertsStayScheduled(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked})
	other := testsupport.OpenSampleAt(t, path, orm.Options{})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	transaction := insertJoinTransaction(t, ctx, db, ada.ID, "5")

	// Another session bumps the transaction's version: the tracked copy is stale.
	stale := mustGet[sample.Transaction](t, ctx, other, transaction.ID)
	note := "elsewhere"
	stale.Note = &note
	if err := orm.Update(ctx, other, stale); err != nil {
		t.Fatal(err)
	}

	local := "local"
	transaction.Note = &local
	ada.Name = "Ada Lovelace"
	mustPersist(t, db, newUser("Linus", "linus@example.com"))

	if _, err := db.Flush(ctx); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010, got %v", err)
	}
	if changes, err := orm.ChangesOf(db, ada); err != nil || len(changes) != 1 || changes[0] != "Name" {
		t.Errorf("the user update that succeeded before the failure is undone: still dirty, got %v %v", changes, err)
	}
	if transaction.Version != 0 {
		t.Errorf("the version is unchanged after the failed update, got %d", transaction.Version)
	}
	if got := rowCount[sample.User](t, other); got != 1 {
		t.Errorf("the insert and the user update were rolled back, %d users", got)
	}
	if !db.HasPendingWork() {
		t.Error("the new user stays scheduled")
	}

	// Refresh is the way back; then everything flushes.
	if err := orm.Refresh(ctx, db, transaction); err != nil {
		t.Fatal(err)
	}
	assertSummary(t, mustFlush(t, ctx, db), 1, 1, 0)
}

func TestFlush_AFailedDeleteIsTrackedAgain(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked})
	other := testsupport.OpenSampleAt(t, path, orm.Options{})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	transaction := insertJoinTransaction(t, ctx, db, ada.ID, "5")
	detail := newDetail(transaction.ID, "Widget")
	if err := orm.Insert(ctx, db, detail); err != nil {
		t.Fatal(err)
	}
	if err := orm.Load(ctx, db, transaction, "Details"); err != nil {
		t.Fatal(err)
	}

	stale := mustGet[sample.Transaction](t, ctx, other, transaction.ID)
	note := "elsewhere"
	stale.Note = &note
	if err := orm.Update(ctx, other, stale); err != nil {
		t.Fatal(err)
	}

	mustRemove(t, db, transaction) // cascades to the loaded detail: detail first, then the stale transaction
	if _, err := db.Flush(ctx); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010 on the stale transaction delete, got %v", err)
	}
	if !db.IsTracked(detail) || !db.IsTracked(transaction) {
		t.Error("the detail deleted before the failure is tracked again, exactly as it was")
	}
	if got := rowCount[sample.TransactionDetail](t, other); got != 1 {
		t.Errorf("the detail row is back (rolled back), %d rows", got)
	}

	if err := orm.Refresh(ctx, db, transaction); err != nil {
		t.Fatal(err)
	}
	assertSummary(t, mustFlush(t, ctx, db), 0, 0, 2)
}

// --- beside the immediate operations ------------------------------------------

func TestUnitOfWork_ImmediateOperationsWorkBesideIt(t *testing.T) {
	ctx := context.Background()
	db := openTrackedWithTables(t)

	scheduled := newUser("Linus", "linus@example.com")
	mustPersist(t, db, scheduled)

	immediate := newUser("Ada", "ada@example.com")
	if err := orm.Insert(ctx, db, immediate); err != nil {
		t.Fatal(err)
	}

	// An instance persisted and then inserted immediately has nothing left to flush: no duplicate row.
	both := newUser("Dennis", "dennis@example.com")
	mustPersist(t, db, both)
	if err := orm.Insert(ctx, db, both); err != nil {
		t.Fatal(err)
	}

	// An immediate update clears the dirt the flush would have written.
	immediate.Name = "Ada Lovelace"
	if err := orm.Update(ctx, db, immediate); err != nil {
		t.Fatal(err)
	}

	assertSummary(t, mustFlush(t, ctx, db), 1, 0, 0)
	if got := rowCount[sample.User](t, db); got != 3 {
		t.Errorf("three users expected, got %d", got)
	}
	if db.HasPendingWork() {
		t.Error("the flush emptied the schedule")
	}
}
