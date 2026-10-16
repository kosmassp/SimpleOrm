package orm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// This file covers Level 3 M1 beyond the conformance/tracking-cases runner:
// the default untracked session, every read path against the identity map, the
// write rules through a second untracked session, and the Go shape (pointer
// results are the tracked ones). The registry entries are test-local — the
// sample's own return values (`User`), which are copies; tracking needs `*User`.

var trackedUsersByEmail = orm.Inline[sample.UsersByEmailArgs, *sample.User](
	"select id, name, email, display_name, created_at, updated_at from users where email = @Email")

var trackedAllUsers = orm.Inline[orm.EmptyArgs, *sample.User](
	"select id, name, email, display_name, created_at, updated_at from users order by id")

// blobItem is a test-local entity with a []byte column: snapshots must compare byte arrays by content.
type blobItem struct {
	ID      int64  `orm:"column,key,generated"`
	Payload []byte `orm:"column"`
}

func (blobItem) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("blob_items")} }

// openPair opens two sessions on one database file: a tracked one and an
// untracked one that plays "another process" (writes the tracked session must
// not see, reads that verify what really reached the row).
func openPair(t *testing.T) (tracked, other *orm.Db) {
	t.Helper()
	path := testsupport.TempDatabase(t)
	tracked = testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked})
	other = testsupport.OpenSampleAt(t, path, orm.Options{})
	return tracked, other
}

func openTracked(t *testing.T) *orm.Db {
	t.Helper()
	return testsupport.OpenSampleWith(t, orm.Options{Tracking: orm.TrackingTracked})
}

func mustGet[T any](t *testing.T, ctx context.Context, db *orm.Db, key any) *T {
	t.Helper()
	entity, err := orm.Get[*T](ctx, db, key)
	if err != nil {
		t.Fatalf("get %T %v: %v", entity, key, err)
	}
	return entity
}

func TestTracking_DefaultSessionIsUntracked(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	if db.IsTracking() {
		t.Error("the default session must not track")
	}
	a := mustGet[sample.User](t, ctx, db, ada.ID)
	b := mustGet[sample.User](t, ctx, db, ada.ID)
	if a == b {
		t.Error("an untracked session materializes a fresh instance per read")
	}
	if db.IsTracked(a) || db.IsTracked(ada) {
		t.Error("nothing is tracked in an untracked session")
	}
	if _, err := orm.IsDirty(db, a); orm.CodeOf(err) != "UOW-001" {
		t.Errorf("IsDirty on an untracked session must be UOW-001, got %v", err)
	}
	if _, err := orm.ChangesOf(db, a); orm.CodeOf(err) != "UOW-001" {
		t.Errorf("ChangesOf on an untracked session must be UOW-001, got %v", err)
	}
}

func TestTracking_EveryReadPathReturnsTheTrackedInstance(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com") // insert registers it

	if !db.IsTracked(ada) {
		t.Fatal("an inserted instance is tracked")
	}

	t.Run("registry query", func(t *testing.T) {
		rows, err := orm.Query(ctx, db, trackedUsersByEmail, sample.UsersByEmailArgs{Email: "ada@example.com"})
		if err != nil || len(rows) != 1 || rows[0] != ada {
			t.Fatalf("expected the tracked instance, got %v %v", rows, err)
		}
		single, err := orm.QuerySingle(ctx, db, trackedUsersByEmail, sample.UsersByEmailArgs{Email: "ada@example.com"})
		if err != nil || single != ada {
			t.Fatalf("QuerySingle: expected the tracked instance, got %v %v", single, err)
		}
	})

	t.Run("stream", func(t *testing.T) {
		count := 0
		for user, err := range orm.Stream(ctx, db, trackedAllUsers, orm.EmptyArgs{}) {
			if err != nil {
				t.Fatal(err)
			}
			count++
			if user != ada {
				t.Errorf("stream yielded a different instance")
			}
		}
		if count != 1 {
			t.Fatalf("expected one row, got %d", count)
		}
	})

	t.Run("key read", func(t *testing.T) {
		if got := mustGet[sample.User](t, ctx, db, ada.ID); got != ada {
			t.Error("Get[*User] must return the tracked instance")
		}
		got, err := orm.GetOrDefault[sample.User](ctx, db, ada.ID)
		if err != nil || got != ada {
			t.Errorf("GetOrDefault must return the tracked instance, got %v %v", got, err)
		}
	})

	t.Run("criteria and select-all", func(t *testing.T) {
		rows, err := orm.From[*sample.User](db).Where(orm.Eq("ID", ada.ID)).List(ctx)
		if err != nil || len(rows) != 1 || rows[0] != ada {
			t.Fatalf("criteria: expected the tracked instance, got %v %v", rows, err)
		}
		single, err := orm.From[*sample.User](db).Where(orm.Eq("ID", ada.ID)).Single(ctx)
		if err != nil || single != ada {
			t.Fatalf("criteria Single: expected the tracked instance, got %v %v", single, err)
		}
		all, err := orm.QueryAll[*sample.User](ctx, db)
		if err != nil || len(all) != 1 || all[0] != ada {
			t.Fatalf("select-all: expected the tracked instance, got %v %v", all, err)
		}
	})

	t.Run("value-shaped results are copies, never registered", func(t *testing.T) {
		ada.Name = "Changed locally"
		copied, err := orm.Get[sample.User](ctx, db, ada.ID)
		if err != nil {
			t.Fatal(err)
		}
		if copied.Name != "Changed locally" {
			t.Errorf("a value-shaped read of a tracked key is a copy of the tracked state, got %q", copied.Name)
		}
		if db.IsTracked(&copied) {
			t.Error("a value-shaped result is never registered")
		}
		ada.Name = "Ada"
	})
}

func TestTracking_EagerLoadingInEveryFetchModeSharesTrackedInstances(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx := insertJoinTransaction(t, ctx, db, ada.ID, "5")
	role := insertRole(t, ctx, db, "admin")
	assignRole(t, ctx, db, ada.ID, role.ID)

	for _, mode := range []orm.FetchMode{orm.FetchMultiQuery, orm.FetchSubSelect, orm.FetchJoin} {
		t.Run(mode.Token(), func(t *testing.T) {
			ada.Name = "Changed locally" // the tracked instance wins: the row is never applied
			users, err := orm.From[*sample.User](db).
				Where(orm.Eq("ID", ada.ID)).
				Include("Transactions").
				Fetch(mode).
				List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(users) != 1 || users[0] != ada {
				t.Fatalf("expected the tracked root instance")
			}
			if len(ada.Transactions) != 1 || ada.Transactions[0] != tx {
				t.Fatalf("expected the tracked transaction in the loaded collection")
			}
			if ada.Name != "Changed locally" {
				t.Errorf("a re-read must not apply the row; Name = %q", ada.Name)
			}

			roles, err := orm.From[*sample.User](db).Where(orm.Eq("ID", ada.ID)).Include("Roles").Fetch(mode).List(ctx)
			if err != nil || len(roles) != 1 || len(roles[0].Roles) != 1 || roles[0].Roles[0] != role {
				t.Fatalf("expected the tracked role through many-to-many, got %v %v", roles, err)
			}

			transactions, err := orm.From[*sample.Transaction](db).Include("User").Fetch(mode).List(ctx)
			if err != nil || len(transactions) != 1 || transactions[0] != tx || tx.User != ada {
				t.Fatalf("expected the tracked transaction with the tracked user, got %v %v", transactions, err)
			}
			ada.Name = "Ada"
		})
	}
}

func TestTracking_ExplicitLoadingSharesTrackedInstances(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx := insertJoinTransaction(t, ctx, db, ada.ID, "5")
	role := insertRole(t, ctx, db, "admin")
	assignRole(t, ctx, db, ada.ID, role.ID)

	if err := orm.Load(ctx, db, tx, "User"); err != nil || tx.User != ada {
		t.Fatalf("many-to-one load: expected the tracked user, got %v %v", tx.User, err)
	}
	if err := orm.Load(ctx, db, ada, "Transactions"); err != nil || len(ada.Transactions) != 1 || ada.Transactions[0] != tx {
		t.Fatalf("one-to-many load: expected the tracked transaction, got %v %v", ada.Transactions, err)
	}
	if err := orm.Load(ctx, db, ada, "Roles"); err != nil || len(ada.Roles) != 1 || ada.Roles[0] != role {
		t.Fatalf("many-to-many load: expected the tracked role, got %v %v", ada.Roles, err)
	}
}

func TestTracking_DirtyUpdateWritesOnlyTheChangedColumns(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	seed := testsupport.InsertUser(t, other, "Ada", "ada@example.com")

	ada := mustGet[sample.User](t, ctx, tracked, seed.ID)

	// Another process changes Email after the tracked read.
	theirs := mustGet[sample.User](t, ctx, other, seed.ID)
	theirs.Email = "new@example.com"
	if err := orm.Update(ctx, other, theirs); err != nil {
		t.Fatal(err)
	}

	ada.Name = "Ada Lovelace"
	changes, err := orm.ChangesOf(tracked, ada)
	if err != nil || len(changes) != 1 || changes[0] != "Name" {
		t.Fatalf("expected [Name], got %v %v", changes, err)
	}
	if err := orm.Update(ctx, tracked, ada); err != nil {
		t.Fatal(err)
	}

	row := mustGet[sample.User](t, ctx, other, seed.ID)
	if row.Name != "Ada Lovelace" {
		t.Errorf("the changed column is written, got %q", row.Name)
	}
	if row.Email != "new@example.com" {
		t.Errorf("a clean column is never written; the other process's Email was overwritten with %q", row.Email)
	}
	if dirty, _ := orm.IsDirty(tracked, ada); dirty {
		t.Error("the snapshot moves forward after the write")
	}
}

func TestTracking_VersionedDirtyUpdateBumpsTheVersionAndCleanUpdateWritesNothing(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	user := testsupport.InsertUser(t, other, "Ada", "ada@example.com")
	seed := insertJoinTransaction(t, ctx, other, user.ID, "5")

	tx := mustGet[sample.Transaction](t, ctx, tracked, seed.ID)
	note := "first"
	tx.Note = &note
	if err := orm.Update(ctx, tracked, tx); err != nil {
		t.Fatal(err)
	}
	if tx.Version != 1 {
		t.Errorf("a dirty update bumps the in-memory version, got %d", tx.Version)
	}
	row := mustGet[sample.Transaction](t, ctx, other, seed.ID)
	if row.Version != 1 || row.Note == nil || *row.Note != "first" {
		t.Errorf("the row must hold version 1 and the note, got %+v", row)
	}

	// Another process moves the version on; a clean update issues no statement, so it cannot conflict.
	if err := orm.UpdateOnly(ctx, other, row, "Status"); err != nil {
		t.Fatal(err)
	}
	if err := orm.Update(ctx, tracked, tx); err != nil {
		t.Fatalf("a clean tracked update must write nothing (no CRUD-010), got %v", err)
	}
	if tx.Version != 1 {
		t.Errorf("a clean update does not bump the version, got %d", tx.Version)
	}
}

func TestTracking_StaleUpdateIsConcurrencyErrorAndRefreshAppliesTheRow(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	user := testsupport.InsertUser(t, other, "Ada", "ada@example.com")
	seed := insertJoinTransaction(t, ctx, other, user.ID, "5")

	tx := mustGet[sample.Transaction](t, ctx, tracked, seed.ID)

	theirs := mustGet[sample.Transaction](t, ctx, other, seed.ID)
	theirNote := "theirs"
	theirs.Note = &theirNote
	if err := orm.Update(ctx, other, theirs); err != nil {
		t.Fatal(err)
	}

	tx.Status = sample.Completed
	err := orm.Update(ctx, tracked, tx)
	if orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010, got %v", err)
	}
	if dirty, _ := orm.IsDirty(tracked, tx); !dirty {
		t.Error("a failed update leaves the instance dirty")
	}

	if err := orm.Refresh(ctx, tracked, tx); err != nil {
		t.Fatal(err)
	}
	if tx.Version != 1 || tx.Note == nil || *tx.Note != "theirs" || tx.Status != sample.Pending {
		t.Errorf("Refresh must apply the row, got %+v", tx)
	}
	if dirty, _ := orm.IsDirty(tracked, tx); dirty {
		t.Error("Refresh resets the snapshot")
	}
}

func TestTracking_ByteArraysCompareByContent(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	if err := orm.CreateTable[blobItem](ctx, db); err != nil {
		t.Fatal(err)
	}

	item := &blobItem{Payload: []byte{1, 2, 3}}
	if err := orm.Insert(ctx, db, item); err != nil {
		t.Fatal(err)
	}
	if dirty, err := orm.IsDirty(db, item); err != nil || dirty {
		t.Fatalf("a just-inserted instance is clean, got %v %v", dirty, err)
	}

	item.Payload[0] = 9 // in place: the snapshot holds a copy, so this is a change
	changes, err := orm.ChangesOf(db, item)
	if err != nil || len(changes) != 1 || changes[0] != "Payload" {
		t.Fatalf("an in-place byte edit must show as a change, got %v %v", changes, err)
	}

	// A different slice with the snapshot's content is not a change: bytes compare by content, never by identity.
	item.Payload = []byte{1, 2, 3}
	if dirty, _ := orm.IsDirty(db, item); dirty {
		t.Error("equal content in another slice must be clean")
	}
}

func TestTracking_ChangedKeyAndForeignInstanceAreRefusedBeforeAnySQL(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	seed := testsupport.InsertUser(t, other, "Ada", "ada@example.com")
	ada := mustGet[sample.User](t, ctx, tracked, seed.ID)

	stranger := &sample.User{ID: seed.ID, Name: "Impostor", Email: "ada@example.com"}
	stranger.CreatedAtUtc = testsupport.SeedTime
	for name, write := range map[string]func() error{
		"update":     func() error { return orm.Update(ctx, tracked, stranger) },
		"updateOnly": func() error { return orm.UpdateOnly(ctx, tracked, stranger, "Name") },
		"delete":     func() error { return orm.DeleteEntity(ctx, tracked, stranger) },
	} {
		if err := write(); orm.CodeOf(err) != "UOW-002" {
			t.Errorf("%s from a foreign instance must be UOW-002, got %v", name, err)
		}
	}

	originalID := ada.ID
	ada.ID = originalID + 100
	for name, write := range map[string]func() error{
		"update":     func() error { return orm.Update(ctx, tracked, ada) },
		"updateOnly": func() error { return orm.UpdateOnly(ctx, tracked, ada, "Name") },
		"delete":     func() error { return orm.DeleteEntity(ctx, tracked, ada) },
	} {
		if err := write(); orm.CodeOf(err) != "UOW-002" {
			t.Errorf("%s with a changed key must be UOW-002, got %v", name, err)
		}
	}

	// Nothing reached the row, and the tracked instance is still the session's.
	row := mustGet[sample.User](t, ctx, other, seed.ID)
	if row.Name != "Ada" {
		t.Errorf("a refused write must not reach the row, got %q", row.Name)
	}
	ada.ID = originalID
	if got := mustGet[sample.User](t, ctx, tracked, seed.ID); got != ada {
		t.Error("the tracked instance survives refused writes")
	}
}

func TestTracking_UpdateOnlyMovesOnlyTheWrittenSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	ada.Name = "Ada Lovelace"
	ada.Email = "lovelace@example.com"
	if err := orm.UpdateOnly(ctx, db, ada, "Name"); err != nil {
		t.Fatal(err)
	}
	changes, err := orm.ChangesOf(db, ada)
	if err != nil || len(changes) != 1 || changes[0] != "Email" {
		t.Fatalf("the unwritten local change must stay dirty, got %v %v", changes, err)
	}
}

func TestTracking_UpdateOfAnUnknownInstanceWritesTheFullRowThenTracksIt(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	seed := testsupport.InsertUser(t, other, "Ada", "ada@example.com")

	known := mustGet[sample.User](t, ctx, other, seed.ID) // not tracked by the tracked session
	known.Name = "Ada Lovelace"
	if err := orm.Update(ctx, tracked, known); err != nil {
		t.Fatal(err)
	}
	if !tracked.IsTracked(known) {
		t.Error("an updated instance is tracked afterwards")
	}
	if dirty, _ := orm.IsDirty(tracked, known); dirty {
		t.Error("tracked clean after its full-row write")
	}
}

func TestTracking_DeleteForgetsTheKey(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")

	if err := orm.DeleteEntity(ctx, db, ada); err != nil {
		t.Fatal(err)
	}
	if db.IsTracked(ada) {
		t.Error("a deleted instance is no longer tracked")
	}
	if err := orm.Delete[sample.User](ctx, db, grace.ID); err != nil {
		t.Fatal(err)
	}
	if db.IsTracked(grace) {
		t.Error("deleting by key forgets the instance tracked for it")
	}
}

func TestTracking_RefreshWorksWithoutTracking(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	ada.Name = "Changed locally"
	if err := orm.Refresh(ctx, db, ada); err != nil {
		t.Fatal(err)
	}
	if ada.Name != "Ada" {
		t.Errorf("Refresh applies the row in an untracked session too, got %q", ada.Name)
	}
	if db.IsTracked(ada) {
		t.Error("Refresh never starts tracking")
	}

	if err := orm.Delete[sample.User](ctx, db, ada.ID); err != nil {
		t.Fatal(err)
	}
	if err := orm.Refresh(ctx, db, ada); orm.CodeOf(err) != "CRUD-001" {
		t.Errorf("Refresh of a vanished row must be CRUD-001, got %v", err)
	}
}

func TestTracking_OwnedMembersSnapshotFlatAndRefreshClearsANullableOwner(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	user := testsupport.InsertUser(t, other, "Ada", "ada@example.com")
	bio := "bio"
	profile := &sample.UserProfile{UserID: user.ID, Bio: &bio, Address: &sample.Address{Street: "1 Way", City: "London"}}
	profile.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, other, profile); err != nil {
		t.Fatal(err)
	}

	p := mustGet[sample.UserProfile](t, ctx, tracked, profile.ID)
	p.Address.City = "Cambridge"
	changes, err := orm.ChangesOf(tracked, p)
	if err != nil || len(changes) != 1 || changes[0] != "Address.City" {
		t.Fatalf("expected [Address.City], got %v %v", changes, err)
	}

	// The row loses its address entirely (all columns NULL): Refresh must clear the nullable owner.
	theirs := mustGet[sample.UserProfile](t, ctx, other, profile.ID)
	theirs.Address = nil
	if err := orm.Update(ctx, other, theirs); err != nil {
		t.Fatal(err)
	}
	if err := orm.Refresh(ctx, tracked, p); err != nil {
		t.Fatal(err)
	}
	if p.Address != nil {
		t.Errorf("an all-NULL owned row refreshes to no owner, got %+v", p.Address)
	}
	if dirty, _ := orm.IsDirty(tracked, p); dirty {
		t.Error("Refresh resets the snapshot")
	}
}

// jsonSettings is the value of a test-local JSON column: a struct a registered JSON handler
// reads and writes, so the snapshot must compare it by what would be written.
type jsonSettings struct {
	Theme string
	Size  int
}

type jsonDocument struct {
	ID       int64        `orm:"column,key,generated"`
	Settings jsonSettings `orm:"column,type=string"`
}

func (jsonDocument) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("json_documents")} }

func TestTracking_JSONHandlerMemberMutatedInPlaceIsDirtyAndWritten(t *testing.T) {
	ctx := context.Background()
	handlers := orm.RegisterJSON[jsonSettings](orm.NewTypeHandlerRegistry())
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked, TypeHandlers: handlers})
	if err := orm.CreateTable[jsonDocument](ctx, db); err != nil {
		t.Fatal(err)
	}

	doc := &jsonDocument{Settings: jsonSettings{Theme: "dark", Size: 12}}
	if err := orm.Insert(ctx, db, doc); err != nil {
		t.Fatal(err)
	}
	if dirty, err := orm.IsDirty(db, doc); err != nil || dirty {
		t.Fatalf("a just-inserted instance is clean, got %v %v", dirty, err)
	}

	doc.Settings.Size = 14
	changes, err := orm.ChangesOf(db, doc)
	if err != nil || len(changes) != 1 || changes[0] != "Settings" {
		t.Fatalf("a mutation of a handler-typed member is a change, got %v %v", changes, err)
	}
	if err := orm.Update(ctx, db, doc); err != nil {
		t.Fatal(err)
	}
	other := testsupport.OpenSampleAt(t, path, orm.Options{TypeHandlers: handlers})
	stored := mustGet[jsonDocument](t, ctx, other, doc.ID)
	if stored.Settings.Size != 14 {
		t.Errorf("the mutated member is written, got %+v", stored.Settings)
	}
	if dirty, _ := orm.IsDirty(db, doc); dirty {
		t.Error("the snapshot moves forward after the write")
	}

	doc.Settings = jsonSettings{Theme: "dark", Size: 14}
	if dirty, _ := orm.IsDirty(db, doc); dirty {
		t.Error("a value that binds the same JSON is clean")
	}
}

func TestTracking_DecimalsEqualInValueAreCleanWhateverTheirText(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx := insertJoinTransaction(t, ctx, db, ada.ID, "19.99")

	tx.Amount = orm.MustDecimal("19.990")
	if dirty, _ := orm.IsDirty(db, tx); dirty {
		t.Error("equal decimals are clean")
	}
	tx.Amount = orm.MustDecimal("20")
	if changes, _ := orm.ChangesOf(db, tx); len(changes) != 1 || changes[0] != "Amount" {
		t.Errorf("a different decimal is a change, got %v", changes)
	}
}

func TestTracking_ChangesOfListsPropertiesInMetadataOrder(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	updated := testsupport.SeedTime
	ada.UpdatedAtUtc = &updated
	ada.Email = "lovelace@example.com"
	ada.Name = "Ada Lovelace"

	changes, err := orm.ChangesOf(db, ada)
	if want := "Name,Email,UpdatedAtUtc"; err != nil || strings.Join(changes, ",") != want {
		t.Fatalf("expected %s (own properties first, inherited after), got %v %v", want, changes, err)
	}

	bio := "bio"
	profile := &sample.UserProfile{UserID: ada.ID, Bio: &bio, Address: &sample.Address{Street: "1 Way", City: "London"}}
	profile.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, profile); err != nil {
		t.Fatal(err)
	}
	profile.UpdatedAtUtc = &updated
	profile.Address.City = "Paris"
	edited := "changed"
	profile.Bio = &edited
	changes, err = orm.ChangesOf(db, profile)
	if want := "Bio,Address.City,UpdatedAtUtc"; err != nil || strings.Join(changes, ",") != want {
		t.Fatalf("expected %s (owned members at their navigation's position), got %v %v", want, changes, err)
	}
}

func TestTracking_RefreshPassesTheIdentityGate(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	ada := testsupport.InsertUser(t, other, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, other, "Grace", "grace@example.com")

	held := mustGet[sample.User](t, ctx, tracked, ada.ID)

	copyOfHeld := &sample.User{ID: ada.ID, Name: "Copy"}
	if err := orm.Refresh(ctx, tracked, copyOfHeld); orm.CodeOf(err) != "UOW-002" {
		t.Errorf("Refresh of a second instance for a held key must be UOW-002, got %v", err)
	}
	if held.Name != "Ada" || copyOfHeld.Name != "Copy" {
		t.Error("a refused Refresh applies nothing")
	}

	held.ID = ada.ID + 100
	if err := orm.Refresh(ctx, tracked, held); orm.CodeOf(err) != "UOW-002" {
		t.Errorf("Refresh of a tracked instance whose key was changed must be UOW-002, got %v", err)
	}
	held.ID = ada.ID

	stranger := &sample.User{ID: grace.ID}
	if err := orm.Refresh(ctx, tracked, stranger); err != nil {
		t.Fatal(err)
	}
	if stranger.Name != "Grace" {
		t.Errorf("an unknown instance with a free key has the row applied, got %q", stranger.Name)
	}
	if tracked.IsTracked(stranger) {
		t.Error("Refresh never registers an instance")
	}
	if got := mustGet[sample.User](t, ctx, tracked, grace.ID); got == stranger {
		t.Error("the refreshed stranger is not the session's instance")
	}
}

func TestTracking_ManyToManyLinkRowsAreTracked(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	seed := testsupport.InsertUser(t, other, "Ada", "ada@example.com")
	role := insertRole(t, ctx, other, "admin")
	assignRole(t, ctx, other, seed.ID, role.ID)

	ada := mustGet[sample.User](t, ctx, tracked, seed.ID)
	if err := orm.Load(ctx, tracked, ada, "Roles"); err != nil {
		t.Fatal(err)
	}

	// The caller never received the link row, yet the loader's materialization registered it.
	link := &sample.UserRole{UserID: seed.ID, RoleID: role.ID}
	link.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Update(ctx, tracked, link); orm.CodeOf(err) != "UOW-002" {
		t.Errorf("the link row a load materialized is the session's instance for its key, got %v", err)
	}
}

func TestTracking_TheEntityFormOfDeleteIsGated(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	if err := orm.DeleteEntity(ctx, db, &sample.User{ID: ada.ID}); orm.CodeOf(err) != "UOW-002" {
		t.Errorf("the entity form of delete is gated, got %v", err)
	}
	if !db.IsTracked(ada) {
		t.Error("a refused delete forgets nothing")
	}
}

func TestTracking_UpdateOnlyOfAnUnknownInstanceDoesNotRegisterIt(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	seed := testsupport.InsertUser(t, other, "Ada", "ada@example.com")

	stranger := &sample.User{ID: seed.ID, Name: "Ada Lovelace"}
	if err := orm.UpdateOnly(ctx, tracked, stranger, "Name"); err != nil {
		t.Fatal(err)
	}
	if tracked.IsTracked(stranger) {
		t.Error("a column-list update saw only part of the row; it must not register the instance")
	}
	if got := mustGet[sample.User](t, ctx, other, seed.ID); got.Name != "Ada Lovelace" {
		t.Errorf("the column is written, got %q", got.Name)
	}
}

func TestTracking_UpdateOnlyMovesTheVersionWithTheWrittenColumns(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx := insertJoinTransaction(t, ctx, db, ada.ID, "19.99")

	note := "checked"
	tx.Note = &note
	tx.Amount = orm.MustDecimal("5")
	if err := orm.UpdateOnly(ctx, db, tx, "Note"); err != nil {
		t.Fatal(err)
	}
	changes, err := orm.ChangesOf(db, tx)
	if err != nil || len(changes) != 1 || changes[0] != "Amount" {
		t.Fatalf("the version moved with the write and the unwritten Amount stays dirty, got %v %v", changes, err)
	}
	if tx.Version != 1 {
		t.Errorf("the version is bumped in memory, got %d", tx.Version)
	}
}

func TestTracking_UpdateOnlyRefusesTheListThenTheNavigationThenTheIdentity(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx := insertJoinTransaction(t, ctx, db, ada.ID, "5")

	foreign := &sample.User{ID: ada.ID}
	if err := orm.UpdateOnly(ctx, db, foreign, "Nope"); orm.CodeOf(err) != "CRUD-005" {
		t.Errorf("an unmapped name is CRUD-005 even for a foreign instance, got %v", err)
	}
	if err := orm.UpdateOnly(ctx, db, foreign, "Name"); orm.CodeOf(err) != "UOW-002" {
		t.Errorf("a valid list from a foreign instance is UOW-002, got %v", err)
	}

	foreignTx := &sample.Transaction{ID: tx.ID, UserID: ada.ID + 100, User: ada}
	if err := orm.UpdateOnly(ctx, db, foreignTx, "UserID"); orm.CodeOf(err) != "CRUD-004" {
		t.Errorf("a navigation that disagrees is CRUD-004, before the identity, got %v", err)
	}
}

func TestTracking_AFullUpdateChecksEveryNavigationBeforeTheIdentityGateAndTheSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	tx := insertJoinTransaction(t, ctx, db, ada.ID, "5")

	// Clean, but the navigation disagrees with the FK: refused, never silently skipped.
	tx.User = grace
	if err := orm.Update(ctx, db, tx); orm.CodeOf(err) != "CRUD-004" {
		t.Errorf("a clean tracked instance with an inconsistent navigation is CRUD-004, got %v", err)
	}

	// A foreign instance with the same inconsistency: CRUD-004 before UOW-002.
	foreign := &sample.Transaction{ID: tx.ID, UserID: ada.ID, User: grace}
	if err := orm.Update(ctx, db, foreign); orm.CodeOf(err) != "CRUD-004" {
		t.Errorf("the navigation check precedes the identity gate on a full update, got %v", err)
	}
}

func TestTracking_AReReadOfATrackedKeyStillMapsTheRow(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	seed := testsupport.InsertUser(t, other, "Ada", "ada@example.com")
	ada := mustGet[sample.User](t, ctx, tracked, seed.ID)

	corruptUser(t, ctx, other, seed.ID)

	if _, err := orm.Get[*sample.User](ctx, tracked, seed.ID); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("the re-read row is still mapped, so its conversion error fires, got %v", err)
	}
	if !tracked.IsTracked(ada) {
		t.Error("the tracked instance survives")
	}
	if dirty, _ := orm.IsDirty(tracked, ada); dirty {
		t.Error("the row's values were never applied")
	}
}

func TestTracking_AReadThatFailsMidwayLeavesResolvedRowsRegistered(t *testing.T) {
	ctx := context.Background()
	tracked, other := openPair(t)
	first := testsupport.InsertUser(t, other, "Ada", "ada@example.com")
	second := testsupport.InsertUser(t, other, "Grace", "grace@example.com")
	corruptUser(t, ctx, other, second.ID)

	var seen *sample.User
	var failure error
	for user, err := range orm.Stream(ctx, tracked, trackedAllUsers, orm.EmptyArgs{}) {
		if err != nil {
			failure = err
			break
		}
		if seen == nil {
			seen = user
		}
	}
	if orm.CodeOf(failure) != "VAL-020" || seen == nil {
		t.Fatalf("expected the second row to fail after the first resolved, got %v %v", seen, failure)
	}
	if !tracked.IsTracked(seen) {
		t.Error("the row resolved before the failure stays registered")
	}
	if got := mustGet[sample.User](t, ctx, tracked, first.ID); got != seen {
		t.Error("a later read returns the instance the failed read registered")
	}
}

func TestTracking_LoadingCorrelatesByTheLiveForeignKeyOfATrackedInstance(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx := insertJoinTransaction(t, ctx, db, ada.ID, "5")

	tx.UserID = ada.ID + 100 // a local edit of the tracked FK
	if err := orm.Load(ctx, db, ada, "Transactions"); err != nil {
		t.Fatal(err)
	}
	if len(ada.Transactions) != 0 {
		t.Errorf("correlation reads the live value, so the edited transaction no longer belongs to Ada, got %d", len(ada.Transactions))
	}
	if changes, _ := orm.ChangesOf(db, tx); len(changes) != 1 || changes[0] != "UserID" {
		t.Errorf("the snapshot plays no part in loading; the edit stays dirty, got %v", changes)
	}
}

type corruptUserArgs struct{ ID int64 }

var corruptUserCreatedAt = orm.InlineCommand[corruptUserArgs](
	"update users set created_at = '2026-08-28 09:30:00' where id = @ID")

// corruptUser stores a created_at without a UTC marker: every read of the row is VAL-020.
func corruptUser(t *testing.T, ctx context.Context, db *orm.Db, id int64) {
	t.Helper()
	if _, err := orm.Execute(ctx, db, corruptUserCreatedAt, corruptUserArgs{ID: id}); err != nil {
		t.Fatalf("corrupt user: %v", err)
	}
}

func TestTracking_InsertWithACallerSuppliedKeyHeldByAnotherInstanceIsRefusedBeforeAnySQL(t *testing.T) {
	ctx := context.Background()
	db := openTracked(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	role := insertRole(t, ctx, db, "admin")
	assignRole(t, ctx, db, ada.ID, role.ID)

	copyOfLink := &sample.UserRole{UserID: ada.ID, RoleID: role.ID}
	copyOfLink.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, copyOfLink); orm.CodeOf(err) != "UOW-002" {
		t.Errorf("a caller-supplied key held by another instance is UOW-002 (not the database's unique violation), got %v", err)
	}
	if db.IsTracked(copyOfLink) {
		t.Error("the refused instance is not tracked")
	}
}
