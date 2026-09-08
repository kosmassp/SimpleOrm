package orm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

func newTransaction(userID int64) *sample.Transaction {
	tx := &sample.Transaction{
		UserID: userID,
		Status: sample.Pending,
		Amount: orm.MustDecimal("10"),
	}
	tx.CreatedAtUtc = testsupport.SeedTime
	return tx
}

func TestUpdate_WritesTheFullRowByKey(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	ada.Name = "Ada Lovelace"
	updated := testsupport.SeedTime
	ada.UpdatedAtUtc = &updated
	if err := orm.Update(ctx, db, ada); err != nil {
		t.Fatal(err)
	}

	loaded, err := orm.Get[sample.User](ctx, db, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "Ada Lovelace" {
		t.Errorf("expected updated name, got %s", loaded.Name)
	}
	if loaded.UpdatedAtUtc == nil || !loaded.UpdatedAtUtc.Equal(testsupport.SeedTime) {
		t.Errorf("expected updated_at, got %v", loaded.UpdatedAtUtc)
	}
}

func TestUpdate_OfMissingRowIsCRUD001(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ghost := &sample.User{ID: 999_999, Name: "Ghost", Email: "ghost@example.com"}
	ghost.CreatedAtUtc = testsupport.SeedTime

	err := orm.Update(ctx, db, ghost)
	if orm.CodeOf(err) != "CRUD-001" {
		t.Fatalf("expected CRUD-001, got %v", err)
	}
}

func TestUpdate_VersionedUpdateIncrementsAndDetectsConflicts(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	if err := orm.Insert(ctx, db, newTransaction(ada.ID)); err != nil {
		t.Fatal(err)
	}

	first, err := orm.From[sample.Transaction](db).Where(orm.Eq("UserID", ada.ID)).Single(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := orm.Get[sample.Transaction](ctx, db, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 0 {
		t.Fatalf("expected version 0, got %d", first.Version)
	}

	first.Status = sample.Completed
	if err := orm.Update(ctx, db, &first); err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 {
		t.Fatalf("expected version bumped to 1 in memory, got %d", first.Version)
	}

	first.Amount = orm.MustDecimal("12")
	if err := orm.Update(ctx, db, &first); err != nil {
		t.Fatal(err)
	}
	if first.Version != 2 {
		t.Fatalf("expected version 2, got %d", first.Version)
	}

	stale.Amount = orm.MustDecimal("99") // still version 0
	err = orm.Update(ctx, db, &stale)
	if orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010, got %v", err)
	}

	current, err := orm.Get[sample.Transaction](ctx, db, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Amount.Equal(orm.MustDecimal("12")) {
		t.Errorf("expected the stale write to change nothing, got %s", current.Amount)
	}
	if current.Version != 2 {
		t.Errorf("expected version 2, got %d", current.Version)
	}
}

func TestDelete_ByKeyAndVersionedDeleteByEntity(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	if err := orm.Delete[sample.User](ctx, db, ada.ID); err != nil {
		t.Fatal(err)
	}
	if missing, err := orm.GetOrDefault[sample.User](ctx, db, ada.ID); err != nil || missing != nil {
		t.Fatalf("expected nil, nil, got %v, %v", missing, err)
	}

	err := orm.Delete[sample.User](ctx, db, ada.ID)
	if orm.CodeOf(err) != "CRUD-001" {
		t.Fatalf("expected CRUD-001, got %v", err)
	}

	// Version-checked delete: a stale entity may not delete the row.
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	if err := orm.Insert(ctx, db, newTransaction(grace.ID)); err != nil {
		t.Fatal(err)
	}
	tx, err := orm.From[sample.Transaction](db).Where(orm.Eq("UserID", grace.ID)).Single(ctx)
	if err != nil {
		t.Fatal(err)
	}
	staleTx, err := orm.Get[sample.Transaction](ctx, db, tx.ID)
	if err != nil {
		t.Fatal(err)
	}

	tx.Status = sample.Cancelled
	if err := orm.Update(ctx, db, &tx); err != nil { // bumps the row to version 1
		t.Fatal(err)
	}

	err = orm.DeleteEntity(ctx, db, &staleTx)
	if orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010, got %v", err)
	}

	if err := orm.DeleteEntity(ctx, db, &tx); err != nil { // fresh version deletes fine
		t.Fatal(err)
	}
	if missing, err := orm.GetOrDefault[sample.Transaction](ctx, db, tx.ID); err != nil || missing != nil {
		t.Fatalf("expected nil, nil, got %v, %v", missing, err)
	}
}

func TestDelete_CompositeKeyByList(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	role := &sample.Role{Name: "auditor"}
	role.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, role); err != nil {
		t.Fatal(err)
	}
	link := &sample.UserRole{UserID: ada.ID, RoleID: role.ID}
	link.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, link); err != nil {
		t.Fatal(err)
	}

	key := []any{ada.ID, role.ID}
	if err := orm.Delete[sample.UserRole](ctx, db, key); err != nil {
		t.Fatal(err)
	}
	if missing, err := orm.GetOrDefault[sample.UserRole](ctx, db, key); err != nil || missing != nil {
		t.Fatalf("expected nil, nil, got %v, %v", missing, err)
	}
}

func TestUpdateOnly_WritesTheListedColumnsAndNothingElse(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	ada.Name = "Ada Lovelace"
	ada.Email = "changed@example.com" // set in memory, not listed
	if err := orm.UpdateOnly(ctx, db, ada, "Name"); err != nil {
		t.Fatal(err)
	}

	loaded, err := orm.Get[sample.User](ctx, db, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "Ada Lovelace" {
		t.Errorf("expected the listed column written, got %s", loaded.Name)
	}
	if loaded.Email != "ada@example.com" {
		t.Errorf("expected the unlisted column untouched, got %s", loaded.Email)
	}
}

func TestUpdateOnly_KeepsRowLevelConcurrencyAndBumpsTheVersion(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	if err := orm.Insert(ctx, db, newTransaction(ada.ID)); err != nil {
		t.Fatal(err)
	}
	first, err := orm.From[sample.Transaction](db).Where(orm.Eq("UserID", ada.ID)).Single(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := orm.Get[sample.Transaction](ctx, db, first.ID)
	if err != nil {
		t.Fatal(err)
	}

	first.Amount = orm.MustDecimal("12")
	if err := orm.UpdateOnly(ctx, db, &first, "Amount"); err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 {
		t.Fatalf("expected version bumped to 1, got %d", first.Version)
	}

	stale.Status = sample.Completed // a disjoint column, still version 0
	if err := orm.UpdateOnly(ctx, db, &stale, "Status"); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010, got %v", err)
	}

	current, err := orm.Get[sample.Transaction](ctx, db, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Amount.Equal(orm.MustDecimal("12")) || current.Status != sample.Pending || current.Version != 1 {
		t.Errorf("stale write changed the row: %+v", current)
	}

	ghost := &sample.User{ID: 999_999, Name: "Ghost", Email: "ghost@example.com"}
	ghost.CreatedAtUtc = testsupport.SeedTime
	if err := orm.UpdateOnly(ctx, db, ghost, "Name"); orm.CodeOf(err) != "CRUD-001" {
		t.Fatalf("expected CRUD-001, got %v", err)
	}
}

func TestUpdateOnly_ValidatesTheListBeforeWriting(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	if err := orm.Insert(ctx, db, newTransaction(ada.ID)); err != nil {
		t.Fatal(err)
	}
	tx, err := orm.From[sample.Transaction](db).Where(orm.Eq("UserID", ada.ID)).Single(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		call       func() error
		wantCode   string
		wantTarget string
	}{
		{"unknown", func() error { return orm.UpdateOnly(ctx, db, ada, "Nope") }, "CRUD-005", "User.Nope"},
		{"column name", func() error { return orm.UpdateOnly(ctx, db, ada, "name") }, "CRUD-005", "User.name"},
		{"key", func() error { return orm.UpdateOnly(ctx, db, ada, "ID") }, "CRUD-006", "User.ID"},
		{"version", func() error { return orm.UpdateOnly(ctx, db, &tx, "Version") }, "CRUD-006", "Transaction.Version"},
		{"empty", func() error { return orm.UpdateOnly(ctx, db, ada) }, "CRUD-007", "User"},
		{"repeat", func() error { return orm.UpdateOnly(ctx, db, ada, "Name", "Name") }, "CRUD-007", "User.Name"},
		{"read-only", func() error {
			return orm.UpdateOnly(ctx, db, &sample.UserTransactionTotal{UserName: "x"}, "UserName")
		}, "CRUD-003", "UserTransactionTotal"},
	}
	for _, tc := range cases {
		err := tc.call()
		if orm.CodeOf(err) != tc.wantCode {
			t.Errorf("%s: expected %s, got %v", tc.name, tc.wantCode, err)
			continue
		}
		var ormErr *orm.Error
		if errors.As(err, &ormErr) && ormErr.Target != tc.wantTarget {
			t.Errorf("%s: expected target %q, got %q", tc.name, tc.wantTarget, ormErr.Target)
		}
	}

	untouched, err := orm.Get[sample.User](ctx, db, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Name != "Ada" {
		t.Errorf("a refused list must write nothing, got %s", untouched.Name)
	}
}

func TestUpdate_GuardsReadOnlySources(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	err := orm.Update(ctx, db, &sample.UserTransactionTotal{UserName: "x"})
	if orm.CodeOf(err) != "CRUD-003" {
		t.Fatalf("expected CRUD-003, got %v", err)
	}

	err = orm.Delete[sample.UserTransactionTotal](ctx, db, int64(1))
	if orm.CodeOf(err) != "CRUD-003" {
		t.Fatalf("expected CRUD-003, got %v", err)
	}
}

// guidDoc is the client-GUID key strategy fixture (CrudTests.cs's GuidDoc).
type guidDoc struct {
	ID    orm.GUID `orm:"column,key"`
	Title string   `orm:"column"`
}

func (guidDoc) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("guid_docs")} }

func TestInsert_ClientGuidKeyStrategyRoundTrips(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	if err := orm.CreateTable[guidDoc](ctx, db); err != nil {
		t.Fatal(err)
	}

	doc := &guidDoc{Title: "spec"}
	if !doc.ID.IsZero() {
		t.Fatalf("expected a zero GUID before insert")
	}
	if err := orm.Insert(ctx, db, doc); err != nil {
		t.Fatal(err)
	}
	if doc.ID.IsZero() {
		t.Fatalf("expected a client-assigned GUID after insert")
	}

	loaded, err := orm.Get[guidDoc](ctx, db, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "spec" {
		t.Errorf("expected spec, got %s", loaded.Title)
	}

	loaded.Title = "spec v2"
	if err := orm.Update(ctx, db, &loaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err := orm.Get[guidDoc](ctx, db, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Title != "spec v2" {
		t.Errorf("expected spec v2, got %s", reloaded.Title)
	}

	if err := orm.Delete[guidDoc](ctx, db, doc.ID); err != nil {
		t.Fatal(err)
	}
	if missing, err := orm.GetOrDefault[guidDoc](ctx, db, doc.ID); err != nil || missing != nil {
		t.Fatalf("expected nil, nil, got %v, %v", missing, err)
	}
}
