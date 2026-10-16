package orm_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// This file covers Level 3 M3's detach and merge (spec/unit-of-work.md "Detach and merge") beyond the
// conformance/merge-cases runner: the tracked-session requirement (UOW-003), the stranger (UOW-001),
// detach cancelling a scheduled parent's cascaded inserts but not a directly persisted child, a detached
// instance's changes never flushing, and merge copying values — owned types whole — but never navigations.

func TestDetachMerge_UntrackedSessionRefusesBothCallsWithUOW003(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	if err := orm.Detach(db, ada); orm.CodeOf(err) != "UOW-003" {
		t.Errorf("Detach: expected UOW-003, got %v", err)
	}
	if _, err := orm.Merge(ctx, db, ada); orm.CodeOf(err) != "UOW-003" {
		t.Errorf("Merge: expected UOW-003, got %v", err)
	}
}

func TestDetach_AnInstanceTheSessionNeitherTracksNorHasScheduledIsUOW001(t *testing.T) {
	db := openTracked(t)
	stranger := newUser("Stranger", "stranger@example.com")
	if err := orm.Detach(db, stranger); orm.CodeOf(err) != "UOW-001" {
		t.Fatalf("expected UOW-001, got %v", err)
	}
}

func TestDetach_ADetachedInstanceKeepsItsValuesAndItsChangesNeverFlush(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	ada := mustGet[sample.User](t, ctx, db, 1)

	ada.Name = "Changed locally"
	if err := orm.Detach(db, ada); err != nil {
		t.Fatal(err)
	}
	if db.IsTracked(ada) || ada.Name != "Changed locally" {
		t.Errorf("a detached instance is not tracked and keeps its values: tracked %v, name %q", db.IsTracked(ada), ada.Name)
	}
	assertSummary(t, mustFlush(t, ctx, db), 0, 0, 0)
	if stored := mustGet[sample.User](t, ctx, db, 1); stored == ada || stored.Name != "Ada" {
		t.Errorf("a later read is a fresh instance of the row: %+v", stored)
	}
}

func TestDetach_ADetachedScheduledParentCancelsItsCascadedInsertsButNotADirectlyPersistedChild(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	cascaded := newDetail(0, "Cascaded")
	direct := newDetail(0, "Persisted by name")
	order := newTransactionFor(ada.ID)
	order.Details = []*sample.TransactionDetail{cascaded, direct}

	mustPersist(t, db, order)
	mustPersist(t, db, direct) // by name as well: it survives its owner's cancellation

	if err := orm.Detach(db, order); err != nil {
		t.Fatal(err)
	}
	if db.IsTracked(order) {
		t.Error("the detached parent is not tracked")
	}
	assertSummary(t, mustFlush(t, ctx, db), 1, 0, 0)
	if rowCount[sample.Transaction](t, db) != 0 {
		t.Error("the detached parent was never inserted")
	}
	if !db.IsTracked(direct) || db.IsTracked(cascaded) || rowCount[sample.TransactionDetail](t, db) != 1 {
		t.Errorf("only the directly persisted child survived (direct tracked %v, cascaded tracked %v)", db.IsTracked(direct), db.IsTracked(cascaded))
	}
}

func TestDetach_ADetachedRemovalIsDroppedAndTheRowStays(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	ada := mustGet[sample.User](t, ctx, db, 1)

	mustRemove(t, db, ada)
	if !db.HasPendingWork() {
		t.Fatal("the removal is scheduled")
	}
	if err := orm.Detach(db, ada); err != nil {
		t.Fatal(err)
	}
	if db.HasPendingWork() {
		t.Error("detaching drops the scheduled removal")
	}
	assertSummary(t, mustFlush(t, ctx, db), 0, 0, 0)
	if rowCount[sample.User](t, db) != 1 {
		t.Error("the row stays")
	}
}

func TestMerge_CopiesMappedValuesButNeverNavigations(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	order := insertJoinTransaction(t, ctx, db, ada.ID, "5")
	if err := orm.Load(ctx, db, order, "User"); err != nil {
		t.Fatal(err)
	}
	if order.User != ada {
		t.Fatalf("the loaded navigation is the tracked user")
	}

	note := "edited elsewhere"
	copyOfOrder := &sample.Transaction{ID: order.ID, UserID: ada.ID, Status: sample.Completed, Amount: orm.MustDecimal("9"), Note: &note}
	copyOfOrder.CreatedAtUtc = order.CreatedAtUtc

	merged, err := orm.Merge(ctx, db, copyOfOrder)
	if err != nil {
		t.Fatal(err)
	}
	if merged != order {
		t.Fatal("merge returns the session's instance for the key")
	}
	if merged.User != ada || copyOfOrder.User != nil {
		t.Errorf("navigations are not merged: target %v, copy %v", merged.User, copyOfOrder.User)
	}
	if merged.Status != sample.Completed || merged.Note == nil || *merged.Note != note || merged.Amount != orm.MustDecimal("9") {
		t.Errorf("mapped values are copied: %+v", merged)
	}
	if merged.Note == copyOfOrder.Note {
		t.Error("a nullable value is copied, not aliased")
	}
	changes, err := orm.ChangesOf(db, merged)
	if err != nil || strings.Join(changes, ",") != "Status,Amount,Note" {
		t.Errorf("the merged instance is dirty where the values differed: %v %v", changes, err)
	}
	if db.IsTracked(copyOfOrder) {
		t.Error("the copy stays untracked")
	}
}

func TestMerge_CopiesAnOwnedTypeWholeSoAnAbsentAddressClearsIt(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	profile := newProfile(ada.ID, &sample.Address{Street: "1 Main", City: "London"})
	if err := orm.Insert(ctx, db, profile); err != nil {
		t.Fatal(err)
	}

	copyOfProfile := newProfile(ada.ID, nil)
	copyOfProfile.ID = profile.ID
	merged, err := orm.Merge(ctx, db, copyOfProfile)
	if err != nil {
		t.Fatal(err)
	}
	if merged != profile || merged.Address != nil {
		t.Fatalf("the copy's absent address clears the tracked one: %+v", merged.Address)
	}
	changes, err := orm.ChangesOf(db, merged)
	if err != nil || strings.Join(changes, ",") != "Address.Street,Address.City" {
		t.Errorf("the owned members that held values are dirty, dotted: %v %v", changes, err)
	}

	// And the other direction: an address on the copy is a value copy, not an alias of the copy's struct.
	withAddress := newProfile(ada.ID, &sample.Address{Street: "2 High", City: "Leeds"})
	withAddress.ID = profile.ID
	if _, err := orm.Merge(ctx, db, withAddress); err != nil {
		t.Fatal(err)
	}
	if profile.Address == withAddress.Address || profile.Address.City != "Leeds" {
		t.Errorf("the owned value is copied: %+v", profile.Address)
	}
}

func TestMerge_AnUnsetKeyIsCRUD002AndTheCopyIsNeverTracked(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)

	unsaved := newUser("New", "new@example.com") // a database-generated key still at zero: not set
	if _, err := orm.Merge(ctx, db, unsaved); orm.CodeOf(err) != "CRUD-002" {
		t.Fatalf("expected CRUD-002, got %v", err)
	}
	if db.IsTracked(unsaved) {
		t.Error("a refused merge tracks nothing")
	}
}

func TestMerge_AnUnknownKeyWithNoRowIsCRUD001(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)

	ghost := newUser("Ghost", "ghost@example.com")
	ghost.ID = 999
	if _, err := orm.Merge(ctx, db, ghost); orm.CodeOf(err) != "CRUD-001" {
		t.Fatalf("expected CRUD-001, got %v", err)
	}
}

func TestMerge_NeedsATableBackedKeyedEntityAtMergeTime(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)

	// A keyed view's copy cannot be merged (CRUD-003, decided at merge time, not "like a table row"); neither
	// can a statement- or procedure-backed one — the same refusal the other write-side calls give.
	for name, merge := range map[string]func() error{
		"a keyed view": func() error {
			_, err := orm.Merge(ctx, db, &sample.UserTransactionTotal{UserID: 1})
			return err
		},
		"a statement": func() error {
			_, err := orm.Merge(ctx, db, &sample.DailySales{})
			return err
		},
		"a procedure": func() error {
			_, err := orm.Merge(ctx, db, &sample.UserActivityReport{})
			return err
		},
	} {
		if err := merge(); orm.CodeOf(err) != "CRUD-003" {
			t.Errorf("%s: expected CRUD-003, got %v", name, err)
		}
	}
}

func TestMerge_AnEmptyClientGuidIsAnUnsetKeyButANaturalZeroKeyIsAKey(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	if err := orm.CreateTable[guidDoc](ctx, db); err != nil {
		t.Fatal(err)
	}

	if _, err := orm.Merge(ctx, db, &guidDoc{Title: "unsaved"}); orm.CodeOf(err) != "CRUD-002" {
		t.Errorf("an empty client-generated GUID is not a key: expected CRUD-002, got %v", err)
	}
	// A natural key's default value is a key: the merge reads the row (and finds none) instead of refusing.
	if _, err := orm.Merge(ctx, db, &sample.UserRole{}); orm.CodeOf(err) != "CRUD-001" {
		t.Errorf("a natural (0, 0) key is a key: expected CRUD-001, got %v", err)
	}
}

func TestMerge_TheSessionsOwnInstanceMergesAsANoOpBeforeAnyKeyCheck(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	ada := mustGet[sample.User](t, ctx, db, 1)

	ada.Name = "Edited"
	ada.ID = 0 // its key is no longer a key value, yet it is still the session's own instance
	merged, err := orm.Merge(ctx, db, ada)
	if err != nil || merged != ada || ada.Name != "Edited" {
		t.Fatalf("the session's own instance returns itself untouched: %v %v", merged, err)
	}
}

func TestMerge_ATrackedTargetScheduledForRemovalStillTakesTheValuesAndAuditColumnsMergeLikeAnyValue(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	ada := mustGet[sample.User](t, ctx, db, 1)
	mustRemove(t, db, ada)

	stamp := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	copyOfAda := newUser("Merged", "ada@example.com")
	copyOfAda.ID = 1
	copyOfAda.UpdatedAtUtc = &stamp
	merged, err := orm.Merge(ctx, db, copyOfAda)
	if err != nil || merged != ada {
		t.Fatalf("the tracked instance is the target even while scheduled for removal: %v %v", merged, err)
	}
	if ada.Name != "Merged" || ada.UpdatedAtUtc == nil || !ada.UpdatedAtUtc.Equal(stamp) || !ada.CreatedAtUtc.Equal(testsupport.SeedTime) {
		t.Errorf("values — the audit columns included — are copied as any value: %+v", ada)
	}
	if ada.UpdatedAtUtc == copyOfAda.UpdatedAtUtc {
		t.Error("nothing is aliased: the stamp pointer is the instance's own")
	}
	if !db.HasPendingWork() {
		t.Error("the removal stays scheduled")
	}
}

func TestDetach_ATrackedOwnerCancelsNothingItScheduledAndItsChildrenStillTakeItsKey(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	order := insertJoinTransaction(t, ctx, db, ada.ID, "5")
	child := newDetail(0, "Cascaded child")
	order.Details = []*sample.TransactionDetail{child}

	mustPersist(t, db, order) // tracked: schedules nothing for itself, cascades to the loaded child
	if err := orm.Detach(db, order); err != nil {
		t.Fatal(err)
	}
	if db.IsTracked(order) || !db.HasPendingWork() {
		t.Fatalf("the owner is forgotten but the child it scheduled stays scheduled")
	}
	assertSummary(t, mustFlush(t, ctx, db), 1, 0, 0)
	if child.TransactionID != order.ID || !db.IsTracked(child) {
		t.Errorf("the child still took its detached owner's key: %+v", child)
	}
}

func TestDetach_ADroppedRemovalLeavesTheRemovalsItsCascadeScheduled(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	order := insertJoinTransaction(t, ctx, db, ada.ID, "5")
	child := newDetail(order.ID, "Child")
	if err := orm.Insert(ctx, db, child); err != nil {
		t.Fatal(err)
	}
	order.Details = []*sample.TransactionDetail{child}

	mustRemove(t, db, order) // Transaction.Details cascades remove: both are scheduled
	if err := orm.Detach(db, order); err != nil {
		t.Fatal(err)
	}
	assertSummary(t, mustFlush(t, ctx, db), 0, 0, 1)
	if rowCount[sample.Transaction](t, db) != 1 || rowCount[sample.TransactionDetail](t, db) != 0 {
		t.Error("only the child's removal flushed; the detached owner's was dropped and its row stays")
	}
}

func TestDetach_IsByInstanceNeverByKey(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	ada := mustGet[sample.User](t, ctx, db, 1)

	sameKey := newUser("Stranger", "stranger@example.com")
	sameKey.ID = ada.ID
	if err := orm.Detach(db, sameKey); orm.CodeOf(err) != "UOW-001" {
		t.Fatalf("an instance that only shares a key is UOW-001, got %v", err)
	}
	if !db.IsTracked(ada) {
		t.Error("the tracked instance for that key is untouched")
	}
}

func TestInsert_AFailedImmediateInsertLeavesCreatedAtUnset(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	duplicate := &sample.User{Name: "Impostor", Email: "ada@example.com"} // users.email is unique
	if err := orm.Insert(ctx, db, duplicate); err == nil {
		t.Fatal("the unique index on users.email must refuse the insert")
	}
	if !duplicate.CreatedAtUtc.IsZero() {
		t.Errorf("a failed write leaves the instance as it was: created_at is %v", duplicate.CreatedAtUtc)
	}
}

func TestHooks_TheClockIsConsultedAfterTheGatesSoTheirRefusalsOutrankVAL020(t *testing.T) {
	ctx := context.Background()
	plusOne := time.FixedZone("UTC+1", 3600)
	badClock := func() time.Time { return time.Date(2026, 8, 29, 1, 0, 0, 0, plusOne) }
	path := testsupport.TempDatabase(t)
	good := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked})
	ada := testsupport.InsertUser(t, good, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, good, "Grace", "grace@example.com")
	order := insertJoinTransaction(t, ctx, good, ada.ID, "5")

	db := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked, Clock: badClock})
	tracked := mustGet[sample.User](t, ctx, db, ada.ID)
	tracked.Name = "Edited"

	if err := orm.UpdateOnly(ctx, db, tracked, "NoSuchProperty"); orm.CodeOf(err) != "CRUD-005" {
		t.Errorf("the list validates before the clock: expected CRUD-005, got %v", err)
	}
	inconsistent := mustGet[sample.Transaction](t, ctx, db, order.ID)
	inconsistent.User = grace // navigation key disagrees with UserID
	if err := orm.Update(ctx, db, inconsistent); orm.CodeOf(err) != "CRUD-004" {
		t.Errorf("the navigation check precedes the clock: expected CRUD-004, got %v", err)
	}
	holder := newUser("Other", "other@example.com")
	holder.ID = ada.ID // not the session's instance for that key
	if err := orm.Update(ctx, db, holder); orm.CodeOf(err) != "UOW-002" {
		t.Errorf("the identity gate precedes the clock: expected UOW-002, got %v", err)
	}
	if err := orm.Update(ctx, db, tracked); orm.CodeOf(err) != "VAL-020" {
		t.Errorf("past the gates the clock is read: expected VAL-020, got %v", err)
	}
}

func newTransactionFor(userID int64) *sample.Transaction {
	order := &sample.Transaction{UserID: userID, Status: sample.Pending, Amount: orm.MustDecimal("5")}
	order.CreatedAtUtc = testsupport.SeedTime
	return order
}
