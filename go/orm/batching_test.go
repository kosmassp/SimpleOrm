package orm_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// This file covers Level 4 item 3 (ADR-0044, spec/session.md "Batching") beyond the conformance/batch-cases runner:
// the option and its argument errors, the question the session answers, and the explicit batch's Go shape —
// package functions beside the immediate calls, Discard for the missing dispose — and its failure handling inside a
// caller's transaction. SQLite is the dialect under test: it has no one-round-trip realization, so every plan here
// runs one statement at a time, reusing prepared commands; the live legs (postgres, sqlserver) run the same
// scenarios through their batchers.

func openBatching(t *testing.T, options orm.Options) *orm.Db {
	t.Helper()
	options.Batching = orm.BatchingAuto
	return testsupport.OpenSampleWith(t, options)
}

func TestOpen_RefusesANegativeMaxStatementsPerMessageAndTakesZeroAsTheDefault(t *testing.T) {
	ctx := context.Background()
	if _, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New(), MaxStatementsPerMessage: -1}); err == nil {
		t.Fatal("a negative statement cap is an argument error at Open")
	}
	if _, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New(), Batching: orm.BatchingMode(7)}); err == nil {
		t.Fatal("an unknown batching mode is an argument error at Open")
	}
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New(), Batching: orm.BatchingAuto})
	if err != nil {
		t.Fatalf("zero is the default cap: %v", err)
	}
	defer db.Close()
}

func TestBatchingInEffect_IsFalseOnSQLiteInEveryModeBecauseItsMessagesRunOneStatementAtATime(t *testing.T) {
	if openBatching(t, orm.Options{}).BatchingInEffect() {
		t.Error("SQLite has no one-round-trip realization: BatchingInEffect is false under Auto")
	}
	if testsupport.OpenSample(t).BatchingInEffect() {
		t.Error("BatchingInEffect is false under Off")
	}
}

func TestBatch_QueuesAndSendsEveryOperationAndTracksTheInsertedInstances(t *testing.T) {
	ctx := context.Background()
	db := openBatching(t, orm.Options{Tracking: orm.TrackingTracked})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")

	linus := newUser("Linus", "linus@example.com")
	role := &sample.Role{Name: "reader"}
	grace.Name = "Grace Hopper"
	link := &sample.UserRole{UserID: ada.ID, RoleID: 1}

	batch := db.Batch()
	for _, queue := range []error{
		orm.BatchInsert(batch, linus),
		orm.BatchInsert(batch, role),
		orm.BatchUpdate(batch, grace),
		orm.BatchInsert(batch, link), // a natural key: the statement returns no result
		orm.BatchDelete[sample.User](batch, ada.ID),
	} {
		if queue != nil {
			t.Fatal(queue)
		}
	}
	result, err := batch.Send(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Affected) != 5 || result.Messages != 1 {
		t.Fatalf("expected five counts in one message, got %+v", result)
	}
	for i, count := range result.Affected {
		if count != 1 {
			t.Errorf("operation %d counted %d, want 1", i, count)
		}
	}
	if linus.ID == 0 || role.ID == 0 || !db.IsTracked(linus) || !db.IsTracked(role) {
		t.Errorf("an inserted instance has its generated key and is tracked: %d, %d", linus.ID, role.ID)
	}
	if dirty, err := orm.IsDirty(db, grace); err != nil || dirty {
		t.Errorf("the update moved the snapshot (dirty %v, %v)", dirty, err)
	}
	if db.IsTracked(ada) {
		t.Error("a deleted instance is forgotten")
	}
	if _, err := orm.GetOrDefault[sample.User](ctx, db, ada.ID); err != nil {
		t.Fatal(err)
	}
}

func TestBatch_AFailureInsideTheCallersTransactionLeavesItOpenAndTheInstancesAsTheyWere(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Batching: orm.BatchingAuto})
	other := testsupport.OpenSampleAt(t, path, orm.Options{})
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	good, duplicate := newUser("Linus", "linus@example.com"), newUser("Impostor", "ada@example.com")
	batch := db.Batch()
	_ = orm.BatchInsert(batch, good)
	_ = orm.BatchInsert(batch, duplicate)
	if _, err := batch.Send(ctx); err == nil {
		t.Fatal("the unique index on users.email must refuse the second insert")
	}
	if good.ID != 0 || duplicate.ID != 0 {
		t.Errorf("a failed send puts the keys back (%d, %d)", good.ID, duplicate.ID)
	}
	if _, err := db.Begin(ctx); orm.CodeOf(err) != "TX-001" {
		t.Fatalf("the send leaves the caller's transaction open, got %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rowCount[sample.User](t, other); got != 1 {
		t.Errorf("the caller's rollback undid what the send wrote, %d users left", got)
	}
}

func TestBatch_AFailedSendThatOwnsItsTransactionAppliesNothing(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Batching: orm.BatchingAuto, MaxStatementsPerMessage: 1})
	other := testsupport.OpenSampleAt(t, path, orm.Options{})
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	first, second, duplicate := newUser("Linus", "linus@example.com"), newUser("Ken", "ken@example.com"), newUser("Impostor", "ada@example.com")
	batch := db.Batch()
	_ = orm.BatchInsert(batch, first)
	_ = orm.BatchInsert(batch, second)
	_ = orm.BatchInsert(batch, duplicate)
	if _, err := batch.Send(ctx); err == nil {
		t.Fatal("the third insert violates the unique index")
	}
	if first.ID != 0 || second.ID != 0 {
		t.Errorf("the messages that ran before the failure are undone in memory (%d, %d)", first.ID, second.ID)
	}
	if got := rowCount[sample.User](t, other); got != 1 {
		t.Errorf("a send that owns its transaction applies nothing, %d users in the database", got)
	}
	if _, err := batch.Send(ctx); orm.CodeOf(err) != "TX-003" {
		t.Errorf("the queue is spent either way, got %v", err)
	}
}

func TestBatch_AFlushUnderAutoUndoesEveryMessageWhenALaterOneFails(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)
	db := testsupport.OpenSampleAt(t, path, orm.Options{Tracking: orm.TrackingTracked, Batching: orm.BatchingAuto, MaxStatementsPerMessage: 1})
	other := testsupport.OpenSampleAt(t, path, orm.Options{})
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	first, second, duplicate := newUser("Linus", "linus@example.com"), newUser("Ken", "ken@example.com"), newUser("Impostor", "ada@example.com")
	mustPersist(t, db, first)
	mustPersist(t, db, second)
	mustPersist(t, db, duplicate)
	if _, err := db.Flush(ctx); err == nil {
		t.Fatal("the third insert violates the unique index")
	}
	if first.ID != 0 || second.ID != 0 || db.IsTracked(first) || db.IsTracked(second) {
		t.Errorf("the earlier messages are undone in memory (ids %d, %d)", first.ID, second.ID)
	}
	if got := rowCount[sample.User](t, other); got != 1 {
		t.Errorf("the flush owned its transaction, %d users in the database", got)
	}
	if !db.HasPendingWork() {
		t.Error("a failed flush keeps the schedule")
	}
	mustRemove(t, db, duplicate)
	assertSummary(t, mustFlush(t, ctx, db), 2, 0, 0)
}

func TestBatch_DiscardSpendsTheBatchLikeADispose(t *testing.T) {
	ctx := context.Background()
	db := openBatching(t, orm.Options{})
	user := newUser("Ada", "ada@example.com")

	batch := db.Batch()
	if err := orm.BatchInsert(batch, user); err != nil {
		t.Fatal(err)
	}
	batch.Discard()
	if user.ID != 0 {
		t.Fatal("a discarded batch sends nothing")
	}
	if err := orm.BatchInsert(batch, user); orm.CodeOf(err) != "TX-003" {
		t.Errorf("queuing on a discarded batch is TX-003, got %v", err)
	}
	if _, err := batch.Send(ctx); orm.CodeOf(err) != "TX-003" {
		t.Errorf("sending a discarded batch is TX-003, got %v", err)
	}
	if rowCount[sample.User](t, db) != 0 {
		t.Error("nothing was ever sent")
	}
}

func TestBatch_ChecksAreTheImmediateCallsAndHappenAtSend(t *testing.T) {
	ctx := context.Background()
	db := openBatching(t, orm.Options{})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	// Queuing checks nothing but the batch's own state: whatever the immediate call would refuse, Send refuses.
	batch := db.Batch()
	if err := orm.BatchUpdateOnly(batch, ada, "NoSuchProperty"); err != nil {
		t.Fatalf("queuing never validates, got %v", err)
	}
	if _, err := batch.Send(ctx); orm.CodeOf(err) != "CRUD-005" {
		t.Errorf("the list is validated at Send, got %v", err)
	}

	batch = db.Batch()
	_ = orm.BatchExecute(batch, orm.InlineCommand[sample.UsersByEmailArgs]("update users set name = 'x'"), sample.UsersByEmailArgs{Email: "a"})
	if _, err := batch.Send(ctx); orm.CodeOf(err) != "PRM-002" {
		t.Errorf("a registry command's args bind at Send (an unused property is PRM-002), got %v", err)
	}

	batch = db.Batch()
	_ = orm.BatchDeleteEntity(batch, &sample.UserProfile{ID: 99})
	result, err := batch.Send(ctx)
	if orm.CodeOf(err) != "CRUD-001" {
		t.Errorf("deleting a missing row is CRUD-001 at Send, got %v (%+v)", err, result)
	}

	if err := orm.BatchInsert[sample.User](db.Batch(), nil); err == nil {
		t.Error("a nil entity is an argument error at queue time")
	}
}

func TestBatch_ACancelledContextSendsNothingAndSpendsTheBatch(t *testing.T) {
	db := openBatching(t, orm.Options{})
	user := newUser("Ada", "ada@example.com")
	batch := db.Batch()
	_ = orm.BatchInsert(batch, user)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := batch.Send(ctx); err == nil || orm.CodeOf(err) != "" {
		t.Errorf("cancellation surfaces as the context's error, got %v", err)
	}
	if user.ID != 0 || rowCount[sample.User](t, db) != 0 {
		t.Error("nothing was sent")
	}
}

func TestBatch_AnEmptySendReturnsAnEmptyResultAndBeginsNoTransaction(t *testing.T) {
	ctx := context.Background()
	db := openBatching(t, orm.Options{})
	tx, err := db.Begin(ctx) // a transaction the empty send must neither commit nor trip over
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.Batch().Send(ctx)
	if err != nil || len(result.Affected) != 0 || result.Messages != 0 {
		t.Fatalf("got %+v, %v", result, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

// cappedDialect reports a parameter cap per message like SQL Server's (rule 3 of the plan), over real SQLite: the
// statements bind five parameters each (a User's non-generated columns), so a cap of twelve holds two of them.
type cappedDialect struct {
	*testsupport.RecordingDialect
	cap int
}

func (d cappedDialect) MaxParametersPerMessage() int { return d.cap }

func TestBatch_TheParameterCapCutsAMessageAndAStatementAloneOverItIsAMessageOfItsOwn(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []struct {
		cap  int
		want []int
	}{
		{cap: 12, want: []int{2, 2, 1}},
		{cap: 5, want: []int{1, 1, 1, 1, 1}}, // exactly one statement fits; the next would take it over
		{cap: 3, want: []int{1, 1, 1, 1, 1}}, // a statement alone over the cap is a message of its own
		{cap: 0, want: []int{5}},             // no cap
	} {
		recorder := testsupport.NewRecordingDialect()
		db := testsupport.OpenSampleWith(t, orm.Options{Dialect: cappedDialect{recorder, scenario.cap}, Batching: orm.BatchingAuto})
		batch := db.Batch()
		for _, name := range []string{"a", "b", "c", "d", "e"} {
			if err := orm.BatchInsert(batch, newUser(name, name+"@example.com")); err != nil {
				t.Fatal(err)
			}
		}
		recorder.Reset()
		result, err := batch.Send(ctx)
		if err != nil {
			t.Fatalf("cap %d: %v", scenario.cap, err)
		}
		if !reflect.DeepEqual(recorder.Messages, scenario.want) || result.Messages != len(scenario.want) {
			t.Errorf("cap %d: messages %v (%d), want %v", scenario.cap, recorder.Messages, result.Messages, scenario.want)
		}
	}
}

func TestBatch_ADialectThatBatchesGetsEachMessageWholeAndTheResultsAreJudgedInOrder(t *testing.T) {
	ctx := context.Background()
	recorder := testsupport.NewRecordingDialect()
	recorder.OneRoundTrip = true
	db := testsupport.OpenSampleWith(t, orm.Options{Dialect: recorder, Batching: orm.BatchingAuto})
	if !db.BatchingInEffect() {
		t.Fatal("a dialect that reports SupportsStatementBatching and carries a StatementBatcher is batching in effect")
	}

	good, other := newUser("Ada", "ada@example.com"), newUser("Grace", "grace@example.com")
	batch := db.Batch()
	_ = orm.BatchInsert(batch, good)
	_ = orm.BatchInsert(batch, other)
	result, err := batch.Send(ctx)
	if err != nil || !reflect.DeepEqual(recorder.Messages, []int{2}) || good.ID == 0 || other.ID != good.ID+1 {
		t.Fatalf("one message of two statements, keys written back in order: %+v, %v, %v (%d, %d)", result, err, recorder.Messages, good.ID, other.ID)
	}

	// A stale update followed by a statement that is fine: both ran, then the first judged CRUD-010.
	stale := &sample.Transaction{ID: 1, Status: sample.Pending, Amount: orm.MustDecimal("1"), UserID: good.ID}
	note := "x"
	stale.Note = &note
	late := &sample.Role{Name: "late"}
	failing := db.Batch()
	_ = orm.BatchUpdateOnly(failing, stale, "Note")
	_ = orm.BatchInsert(failing, late)
	if _, err := failing.Send(ctx); orm.CodeOf(err) != "CRUD-001" && orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected the zero-row refusal, got %v", err)
	}
	if late.ID != 0 || rowCount[sample.Role](t, db) != 0 {
		t.Errorf("the insert after the failing statement ran inside the send's transaction and was undone (key %d)", late.ID)
	}
}

// orderedDialect records, in one sequence, when a statement is rendered (prepared) and when a message goes out.
type orderedDialect struct {
	*testsupport.RecordingDialect
	events *[]string
}

func (d orderedDialect) ObserveWriteRendering(op string, m *orm.EntityMap, all bool, properties []*orm.PropertyMap) {
	if op == "insert" {
		*d.events = append(*d.events, "prepare")
	}
	d.RecordingDialect.ObserveWriteRendering(op, m, all, properties)
}

func (d orderedDialect) ObserveMessage(statements int) {
	*d.events = append(*d.events, "message of "+string(rune('0'+statements)))
	d.RecordingDialect.ObserveMessage(statements)
}

func TestBatch_AFullMessageGoesOutBeforeTheNextStatementIsPreparedAndUnderOffEveryStatementDoes(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []struct {
		name    string
		options orm.Options
		want    []string
	}{
		{"cap of two", orm.Options{Batching: orm.BatchingAuto, MaxStatementsPerMessage: 2},
			[]string{"prepare", "prepare", "message of 2", "prepare", "message of 1"}},
		{"off", orm.Options{},
			[]string{"prepare", "message of 1", "prepare", "message of 1", "prepare", "message of 1"}},
	} {
		var events []string
		dialect := orderedDialect{testsupport.NewRecordingDialect(), &events}
		scenario.options.Dialect = dialect
		db := testsupport.OpenSampleWith(t, scenario.options)
		batch := db.Batch()
		for _, name := range []string{"a", "b", "c"} {
			_ = orm.BatchInsert(batch, &sample.Role{Name: name})
		}
		events = nil
		if _, err := batch.Send(ctx); err != nil {
			t.Fatalf("%s: %v", scenario.name, err)
		}
		if !reflect.DeepEqual(events, scenario.want) {
			t.Errorf("%s: events %v, want %v", scenario.name, events, scenario.want)
		}
	}
}

func TestFlush_TwoNewInstancesWithOneNaturalKeyRefuseTheSecondWithUOW002InEveryMode(t *testing.T) {
	ctx := context.Background()
	for name, mode := range map[string]orm.BatchingMode{"off": orm.BatchingOff, "auto": orm.BatchingAuto} {
		db := testsupport.OpenSampleWith(t, orm.Options{Tracking: orm.TrackingTracked, Batching: mode})
		first, second := &sample.UserRole{UserID: 1, RoleID: 1}, &sample.UserRole{UserID: 1, RoleID: 1}
		mustPersist(t, db, first)
		mustPersist(t, db, second)
		if _, err := db.Flush(ctx); orm.CodeOf(err) != "UOW-002" {
			t.Errorf("%s: the second instance meets the identity gate, not the database's key violation, got %v", name, err)
		}
		if db.IsTracked(first) || rowCount[sample.UserRole](t, db) != 0 {
			t.Errorf("%s: a failed flush applies nothing", name)
		}
	}
}
