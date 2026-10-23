package testsupport

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// The batching scenarios every implementation's live legs run (Level 4 item 3, ADR-0044, spec/dialects.md "Testing
// contract"): a batched flush whose generated keys flow across messages, an explicit batch carrying every operation,
// a stale version in the middle of a message, a failing statement in the middle of a message, the caller's
// transaction, and a registry command among generated writes. One body per scenario, so the PostgreSQL leg (pgx's
// pipeline), the SQL Server leg (one multi-statement text), and SQLite (the sequential realization) are judged by the
// same assertions; a leg only says how to open a session and what its database does that the others do not.

// ObservingDialect decorates a dialect to tell the session's test hook (core.MessageObserver) about every message,
// and forwards the one-round-trip capability the embedded interface would otherwise hide. Everything else delegates.
type ObservingDialect struct {
	orm.Dialect
	// Sizes is the statement count of every message since the last Reset, in order.
	Sizes []int
}

// ObserveMessage records one message's statement count.
func (d *ObservingDialect) ObserveMessage(statements int) { d.Sizes = append(d.Sizes, statements) }

// Reset forgets the messages seen so far.
func (d *ObservingDialect) Reset() { d.Sizes = nil }

// SendMessage forwards to the wrapped dialect's StatementBatcher; the session asks for the capability only where
// the wrapped dialect reports SupportsStatementBatching, so the error is unreachable.
func (d *ObservingDialect) SendMessage(ctx context.Context, conn *sql.Conn, exec core.SQLExecutor, statements []core.MessageStatement) ([]core.MessageResult, error) {
	batcher, ok := d.Dialect.(core.StatementBatcher)
	if !ok {
		return nil, errors.New("the wrapped dialect has no StatementBatcher")
	}
	return batcher.SendMessage(ctx, conn, exec, statements)
}

// BatchingLeg is one dialect's way into the scenarios.
type BatchingLeg struct {
	// Open opens a session with the given options on a fresh database holding the sample tables, over an
	// ObservingDialect that wraps the dialect under test.
	Open func(t *testing.T, options orm.Options) (*orm.Db, *ObservingDialect)
	// BatchingInEffect: the dialect's messages go out as one round trip, so the statements after a failing check run.
	BatchingInEffect bool
	// IdentityIsNotRolledBack: a rolled-back insert still consumes an identity value (PostgreSQL, SQL Server; not SQLite's
	// rowid), which is how a scenario sees that a statement after a failing one ran.
	IdentityIsNotRolledBack bool
}

type setDisplayNameArgs struct {
	ID          int64
	DisplayName *string
}

var setDisplayName = orm.InlineCommand[setDisplayNameArgs]("update users set display_name = @DisplayName where id = @Id")

var renameTwoUsers = orm.InlineCommand[orm.EmptyArgs](
	"update users set display_name = 'first' where name = 'Ada'; update users set display_name = 'second' where name = 'Grace'")

func openBatching(t *testing.T, leg BatchingLeg, options orm.Options) (*orm.Db, *ObservingDialect) {
	t.Helper()
	options.Batching = orm.BatchingAuto
	db, observer := leg.Open(t, options)
	if db.BatchingInEffect() != leg.BatchingInEffect {
		t.Fatalf("BatchingInEffect() = %v, the leg expects %v", db.BatchingInEffect(), leg.BatchingInEffect)
	}
	return db, observer
}

func total(sizes []int) int {
	sum := 0
	for _, size := range sizes {
		sum += size
	}
	return sum
}

func sameSizes(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func count[T any](t *testing.T, db *orm.Db) int {
	t.Helper()
	rows, err := orm.QueryAll[T](context.Background(), db)
	if err != nil {
		t.Fatalf("select-all: %v", err)
	}
	return len(rows)
}

func pending(amount string, user *sample.User) *sample.Transaction {
	return &sample.Transaction{Status: sample.Pending, Amount: orm.MustDecimal(amount), User: user}
}

// BatchedFlush: two users and their transactions flush in two messages — the transactions read their users' generated
// keys, so they start the second — and the keys flowed; then a cap of two statements cuts five independent inserts
// into messages of 2, 2, and 1.
func BatchedFlush(t *testing.T, leg BatchingLeg) {
	t.Helper()
	ctx := context.Background()
	db, observer := openBatching(t, leg, orm.Options{Tracking: orm.TrackingTracked})

	u1, u2 := &sample.User{Name: "Linus", Email: "linus@example.com"}, &sample.User{Name: "Ken", Email: "ken@example.com"}
	t1, t2 := pending("5", u1), pending("7", u2)
	for _, persist := range []error{orm.Persist(db, u1), orm.Persist(db, u2), orm.Persist(db, t1), orm.Persist(db, t2)} {
		if persist != nil {
			t.Fatal(persist)
		}
	}
	summary, err := db.Flush(ctx)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if summary != (orm.FlushSummary{Inserted: 4}) {
		t.Errorf("summary = %+v, want four inserts", summary)
	}
	if !sameSizes(observer.Sizes, []int{2, 2}) {
		t.Errorf("messages = %v, want [2 2]: the transactions wait for their users' generated keys", observer.Sizes)
	}
	if u1.ID == 0 || u2.ID == 0 || t1.UserID != u1.ID || t2.UserID != u2.ID || t1.ID == 0 || t2.ID == 0 {
		t.Errorf("generated keys flow across messages: users %d, %d; transactions %d (user %d), %d (user %d)",
			u1.ID, u2.ID, t1.ID, t1.UserID, t2.ID, t2.UserID)
	}
	if count[sample.User](t, db) != 2 || count[sample.Transaction](t, db) != 2 {
		t.Error("the rows reached the database")
	}

	capped, cappedObserver := openBatching(t, leg, orm.Options{Tracking: orm.TrackingTracked, MaxStatementsPerMessage: 2})
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if err := orm.Persist(capped, &sample.Role{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := capped.Flush(ctx); err != nil {
		t.Fatalf("capped flush: %v", err)
	}
	if !sameSizes(cappedObserver.Sizes, []int{2, 2, 1}) {
		t.Errorf("messages = %v, want [2 2 1] under a cap of two", cappedObserver.Sizes)
	}
}

// ExplicitBatchEveryOperation: an insert with a generated key, an insert with a natural key, a full update, a column-list
// update, a delete by key (a soft delete), a version-checked delete by entity, and a registry command, in one send.
func ExplicitBatchEveryOperation(t *testing.T, leg BatchingLeg) {
	t.Helper()
	ctx := context.Background()
	db, observer := openBatching(t, leg, orm.Options{Tracking: orm.TrackingTracked})

	ada, grace := InsertUser(t, db, "Ada", "ada@example.com"), InsertUser(t, db, "Grace", "grace@example.com")
	target, doomed := pendingFor("10", ada.ID), pendingFor("20", ada.ID)
	temp := &sample.Role{Name: "temp"}
	for _, insert := range []error{orm.Insert(ctx, db, target), orm.Insert(ctx, db, doomed), orm.Insert(ctx, db, temp)} {
		if insert != nil {
			t.Fatal(insert)
		}
	}

	linus := &sample.User{Name: "Linus", Email: "linus@example.com"}
	role := &sample.Role{Name: "reader"}
	link := &sample.UserRole{UserID: ada.ID, RoleID: temp.ID}
	grace.Name = "Grace Hopper"
	note := "shipped"
	target.Note = &note
	display := "Ada Lovelace"

	batch := db.Batch()
	for _, queue := range []error{
		orm.BatchInsert(batch, linus),
		orm.BatchInsert(batch, role),
		orm.BatchUpdate(batch, grace),
		orm.BatchUpdateOnly(batch, target, "Note"),
		orm.BatchInsert(batch, link),
		orm.BatchExecute(batch, setDisplayName, setDisplayNameArgs{ID: ada.ID, DisplayName: &display}),
		orm.BatchDelete[sample.Role](batch, temp.ID),
		orm.BatchDeleteEntity(batch, doomed),
	} {
		if queue != nil {
			t.Fatal(queue)
		}
	}
	observer.Reset()
	result, err := batch.Send(ctx)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(result.Affected) != 8 {
		t.Fatalf("one count per operation, got %v", result.Affected)
	}
	for i, affected := range result.Affected {
		if affected != 1 {
			t.Errorf("operation %d counted %d, want 1", i+1, affected)
		}
	}
	if total(observer.Sizes) != 8 || result.Messages != len(observer.Sizes) {
		t.Errorf("eight statements in %d messages, the session saw %v", result.Messages, observer.Sizes)
	}
	if linus.ID == 0 || role.ID == 0 || !db.IsTracked(linus) {
		t.Errorf("an inserted instance has its key and is tracked (%d, %d)", linus.ID, role.ID)
	}
	if target.Version != 1 || db.IsTracked(doomed) {
		t.Errorf("the column-list update bumped the version (%d) and the deleted instance is forgotten (%v)", target.Version, db.IsTracked(doomed))
	}

	// The database holds what the send wrote: refresh re-reads by key.
	if err := orm.Refresh(ctx, db, ada); err != nil || ada.DisplayName == nil || *ada.DisplayName != display {
		t.Errorf("the registry command renamed Ada: %v (%v)", ada.DisplayName, err)
	}
	if err := orm.Refresh(ctx, db, grace); err != nil || grace.Name != "Grace Hopper" {
		t.Errorf("the update renamed Grace: %q (%v)", grace.Name, err)
	}
	if err := orm.Refresh(ctx, db, temp); err != nil || !temp.IsDeleted {
		t.Errorf("the by-key delete flagged the role: %v (%v)", temp.IsDeleted, err)
	}
	if gone, err := orm.GetOrDefault[sample.Transaction](ctx, db, doomed.ID); err != nil || gone != nil {
		t.Errorf("the version-checked delete removed the row: %v (%v)", gone, err)
	}
	if count[sample.UserRole](t, db) != 1 {
		t.Error("the natural-key insert reached the database")
	}
}

// StaleVersionMidMessage: a stale version is CRUD-010 and nothing applies, every instance as it was. Where the messages
// go out as one round trip the insert queued after the stale update ran all the same (a zero-row update is no database
// error): the identity sequence it consumed is the observable.
func StaleVersionMidMessage(t *testing.T, leg BatchingLeg) {
	t.Helper()
	ctx := context.Background()
	db, _ := openBatching(t, leg, orm.Options{})

	owner := InsertUser(t, db, "Ada", "ada@example.com")
	fresh, stale := pendingFor("10", owner.ID), pendingFor("10", owner.ID)
	if err := orm.Insert(ctx, db, fresh); err != nil {
		t.Fatal(err)
	}
	stale.ID, stale.Version = fresh.ID, fresh.Version
	first := &sample.Role{Name: "seed"}
	if err := orm.Insert(ctx, db, first); err != nil {
		t.Fatal(err)
	}

	changed, other := "first", "second"
	fresh.Note = &changed
	if err := orm.UpdateOnly(ctx, db, fresh, "Note"); err != nil {
		t.Fatal(err)
	}
	stale.Note = &other
	late := &sample.Role{Name: "late"}

	batch := db.Batch()
	_ = orm.BatchUpdateOnly(batch, stale, "Note")
	_ = orm.BatchInsert(batch, late)
	if _, err := batch.Send(ctx); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("a stale version is CRUD-010, got %v", err)
	}
	if late.ID != 0 || stale.Version != 0 || stale.UpdatedAtUtc != nil {
		t.Errorf("every instance as it was: late key %d, stale version %d, stale stamp %v", late.ID, stale.Version, stale.UpdatedAtUtc)
	}
	if count[sample.Role](t, db) != 1 {
		t.Error("nothing applied: the role queued after the stale update was rolled back with the send's transaction")
	}

	after := &sample.Role{Name: "after"}
	if err := orm.Insert(ctx, db, after); err != nil {
		t.Fatal(err)
	}
	want := first.ID + 1
	if leg.BatchingInEffect && leg.IdentityIsNotRolledBack {
		want = first.ID + 2 // the insert after the stale update ran inside the rolled-back transaction and consumed a value
	}
	if after.ID != want {
		t.Errorf("the next role got key %d, want %d (batching in effect: %v)", after.ID, want, leg.BatchingInEffect)
	}
}

// FailingStatementMidMessage: the database refuses the middle statement; the provider's error propagates unchanged,
// nothing applies, the instances are as they were, and the session is usable afterwards.
func FailingStatementMidMessage(t *testing.T, leg BatchingLeg) {
	t.Helper()
	ctx := context.Background()
	db, _ := openBatching(t, leg, orm.Options{})
	InsertUser(t, db, "Ada", "ada@example.com")

	first, duplicate, last := &sample.User{Name: "Linus", Email: "linus@example.com"},
		&sample.User{Name: "Impostor", Email: "ada@example.com"}, &sample.User{Name: "Ken", Email: "ken@example.com"}
	batch := db.Batch()
	_ = orm.BatchInsert(batch, first)
	_ = orm.BatchInsert(batch, duplicate)
	_ = orm.BatchInsert(batch, last)
	_, err := batch.Send(ctx)
	if err == nil {
		t.Fatal("the unique index on users.email must refuse the second insert")
	}
	if code := orm.CodeOf(err); code != "" {
		t.Errorf("a provider error propagates unchanged, got the SimpleOrm code %s", code)
	}
	if first.ID != 0 || duplicate.ID != 0 || last.ID != 0 {
		t.Errorf("the keys are put back: %d, %d, %d", first.ID, duplicate.ID, last.ID)
	}
	if count[sample.User](t, db) != 1 {
		t.Error("nothing applied")
	}
	if err := orm.Insert(ctx, db, first); err != nil || count[sample.User](t, db) != 2 {
		t.Errorf("the session is usable after the failed send: %v", err)
	}
}

// InTheCallersTransaction: a send inside the caller's transaction leaves it open — committed or rolled back as the
// caller says — and a failed send leaves the rows of the statements that ran to the caller's rollback.
func InTheCallersTransaction(t *testing.T, leg BatchingLeg) {
	t.Helper()
	ctx := context.Background()
	db, _ := openBatching(t, leg, orm.Options{})
	InsertUser(t, db, "Ada", "ada@example.com")

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch := db.Batch()
	_ = orm.BatchInsert(batch, &sample.User{Name: "Linus", Email: "linus@example.com"})
	_ = orm.BatchInsert(batch, &sample.User{Name: "Ken", Email: "ken@example.com"})
	if _, err := batch.Send(ctx); err != nil {
		t.Fatalf("send inside the caller's transaction: %v", err)
	}
	if _, err := db.Begin(ctx); orm.CodeOf(err) != "TX-001" {
		t.Fatalf("the send leaves the caller's transaction open, got %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if count[sample.User](t, db) != 1 {
		t.Error("the caller's rollback undid the send")
	}

	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failing := db.Batch()
	_ = orm.BatchInsert(failing, &sample.User{Name: "Grace", Email: "grace@example.com"})
	_ = orm.BatchInsert(failing, &sample.User{Name: "Impostor", Email: "ada@example.com"})
	if _, err := failing.Send(ctx); err == nil {
		t.Fatal("the unique index on users.email must refuse the duplicate")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("the caller rolls the failed send back: %v", err)
	}
	if count[sample.User](t, db) != 1 {
		t.Error("the caller's rollback undid the rows the failed send had written")
	}
}

// RegistryCommandAmongGeneratedWrites: a registry command — one with a single statement, and one whose SQL holds
// two — between generated inserts; the results come back in queue order and every effect lands.
func RegistryCommandAmongGeneratedWrites(t *testing.T, leg BatchingLeg) {
	t.Helper()
	ctx := context.Background()
	db, observer := openBatching(t, leg, orm.Options{})
	ada, grace := InsertUser(t, db, "Ada", "ada@example.com"), InsertUser(t, db, "Grace", "grace@example.com")

	display := "Ada L."
	role, second := &sample.Role{Name: "reader"}, &sample.Role{Name: "writer"}
	batch := db.Batch()
	_ = orm.BatchInsert(batch, role)
	_ = orm.BatchExecute(batch, setDisplayName, setDisplayNameArgs{ID: ada.ID, DisplayName: &display})
	_ = orm.BatchInsert(batch, second) // reads a generated key after a command: the plan starts a message for it
	_ = orm.BatchExecute(batch, renameTwoUsers, orm.EmptyArgs{})
	observer.Reset()
	result, err := batch.Send(ctx)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(result.Affected) != 4 || result.Affected[0] != 1 || result.Affected[1] != 1 || result.Affected[2] != 1 || result.Affected[3] < 1 {
		t.Errorf("counts in queue order, got %v", result.Affected)
	}
	if role.ID == 0 || second.ID == 0 || second.ID == role.ID {
		t.Errorf("both inserts read their generated keys: %d, %d", role.ID, second.ID)
	}
	if total(observer.Sizes) != 4 {
		t.Errorf("four statements, the session saw %v", observer.Sizes)
	}
	// The multi-statement command ran whole: it renamed both users after the first command renamed Ada.
	for _, check := range []struct {
		user *sample.User
		want string
	}{{ada, "first"}, {grace, "second"}} {
		got, err := orm.Get[sample.User](ctx, db, check.user.ID)
		if err != nil || got.DisplayName == nil || *got.DisplayName != check.want {
			t.Errorf("%s display name = %v (%v), want %s", check.user.Name, got.DisplayName, err, check.want)
		}
	}
}

// pendingFor is a pending transaction of an existing user, for the immediate inserts: the foreign key is what is
// written, so it is set, and no navigation disagrees with it.
func pendingFor(amount string, userID int64) *sample.Transaction {
	return &sample.Transaction{Status: sample.Pending, Amount: orm.MustDecimal(amount), UserID: userID}
}
