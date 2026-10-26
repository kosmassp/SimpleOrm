package orm_test

import (
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// This file covers Level 4 item 4 (ADR-0045, spec/session.md "Statement log"): every execution path writes exactly one
// line with its name, a flush and an explicit batch write one per statement under Off and Auto, values appear only when
// asked, nothing is written when the option is unset, and the built-in sink writes to standard output. The lines of
// the migration tooling are covered through the CLI (orm/cli/log_test.go) and of SQL Server's multi-statement text in
// orm/sqlserver/batch_test.go.

// lineSink collects the lines a session writes.
type lineSink struct{ lines []string }

func (s *lineSink) write(line string) { s.lines = append(s.lines, line) }

// take returns the lines written since the last take.
func (s *lineSink) take() []string {
	lines := s.lines
	s.lines = nil
	return lines
}

func openLogged(t *testing.T, sink *lineSink, options orm.Options) *orm.Db {
	t.Helper()
	options.Log = sink.write
	return testsupport.OpenSampleWith(t, options)
}

// logNames are the statement names of lines (the text before the first ": ").
func logNames(lines []string) []string {
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i], _, _ = strings.Cut(line, ": ")
	}
	return result
}

// expectNames runs operation and asserts it wrote exactly one line per wanted name, in order.
func expectNames(t *testing.T, sink *lineSink, label string, operation func(), want ...string) []string {
	t.Helper()
	sink.take()
	operation()
	lines := sink.take()
	if got := logNames(lines); !(len(got) == 0 && len(want) == 0) && !reflect.DeepEqual(got, want) {
		t.Errorf("%s: wrote %v, want names %v", label, lines, want)
	}
	return lines
}

var markArgs = sample.MarkTransactionStatusArgs{ID: 99, Status: sample.TransactionStatus("Pending"), UpdatedAt: testsupport.SeedTime}

func TestStatementLog_EveryExecutionPathWritesOneLineWithItsName(t *testing.T) {
	ctx := context.Background()
	sink := &lineSink{}
	db := openLogged(t, sink, orm.Options{Tracking: orm.TrackingTracked})

	// Metadata DDL (written while the fixture creates its tables, through CreateTable/CreateView): one line each.
	setup := strings.Join(logNames(sink.take()), " ")
	for _, want := range []string{"User.createTable", "User.createIndex", "UserTransactionTotal.createView"} {
		if !strings.Contains(setup, want) {
			t.Errorf("the metadata DDL writes %s, got: %s", want, setup)
		}
	}
	triggerLines := expectNames(t, sink, "CreateTriggers", func() {
		if err := orm.CreateTriggers[sample.Role](ctx, db); err != nil {
			t.Fatal(err)
		}
	}, "Role.createTrigger", "Role.createTrigger", "Role.createTrigger", "Role.createTrigger", "Role.createTrigger")
	if !strings.Contains(triggerLines[0], "drop trigger") {
		t.Errorf("the drop statements are logged too, before the create: %v", triggerLines)
	}

	user := newUser("Ada", "ada@example.com")
	expectNames(t, sink, "Insert", func() {
		if err := orm.Insert(ctx, db, user); err != nil {
			t.Fatal(err)
		}
	}, "User.insert")

	expectNames(t, sink, "a clean tracked Update sends nothing", func() {
		if err := orm.Update(ctx, db, user); err != nil {
			t.Fatal(err)
		}
	})
	user.Name = "Ada L."
	expectNames(t, sink, "a dirty tracked Update", func() {
		if err := orm.Update(ctx, db, user); err != nil {
			t.Fatal(err)
		}
	}, "User.update")
	user.Name = "Ada Lovelace"
	expectNames(t, sink, "UpdateOnly", func() {
		if err := orm.UpdateOnly(ctx, db, user, "Name"); err != nil {
			t.Fatal(err)
		}
	}, "User.updateOnly")

	expectNames(t, sink, "Get", func() {
		if _, err := orm.Get[sample.User](ctx, db, user.ID); err != nil {
			t.Fatal(err)
		}
	}, "User.get")
	expectNames(t, sink, "GetOrDefault", func() {
		if _, err := orm.GetOrDefault[sample.User](ctx, db, user.ID); err != nil {
			t.Fatal(err)
		}
	}, "User.get")
	expectNames(t, sink, "Refresh", func() {
		if err := orm.Refresh(ctx, db, user); err != nil {
			t.Fatal(err)
		}
	}, "User.get")
	expectNames(t, sink, "QueryAll", func() {
		if _, err := orm.QueryAll[sample.User](ctx, db); err != nil {
			t.Fatal(err)
		}
	}, "User.selectAll")
	expectNames(t, sink, "criteria", func() {
		if _, err := orm.From[sample.User](db).Where(orm.Eq("Name", "Ada Lovelace")).List(ctx); err != nil {
			t.Fatal(err)
		}
	}, "User.criteria")

	// Registry entries: the kind of entry, the declared name where the application states one.
	byEmail := sample.UsersByEmail
	args := sample.UsersByEmailArgs{Email: "ada@example.com"}
	expectNames(t, sink, "Query", func() {
		if _, err := orm.Query(ctx, db, byEmail, args); err != nil {
			t.Fatal(err)
		}
	}, "query")
	expectNames(t, sink, "QuerySingle", func() {
		if _, err := orm.QuerySingle(ctx, db, byEmail, args); err != nil {
			t.Fatal(err)
		}
	}, "query")
	expectNames(t, sink, "Stream", func() {
		for _, err := range orm.Stream(ctx, db, byEmail, args) {
			if err != nil {
				t.Fatal(err)
			}
		}
	}, "query")
	expectNames(t, sink, "a Named entry", func() {
		if _, err := orm.Query(ctx, db, byEmail.Named("Queries.UsersByEmail"), args); err != nil {
			t.Fatal(err)
		}
	}, "Queries.UsersByEmail")
	expectNames(t, sink, "Execute", func() {
		if _, err := orm.Execute(ctx, db, sample.MarkTransactionStatus, markArgs); err != nil {
			t.Fatal(err)
		}
	}, "command")
	expectNames(t, sink, "a Named command", func() {
		if _, err := orm.Execute(ctx, db, sample.MarkTransactionStatus.Named("Commands.MarkStatus"), markArgs); err != nil {
			t.Fatal(err)
		}
	}, "Commands.MarkStatus")
	expectNames(t, sink, "a statement-backed entity", func() {
		if _, err := orm.QueryStatement[sample.DailySales](ctx, db, sample.DailySalesArgs{Since: testsupport.SeedTime}); err != nil {
			t.Fatal(err)
		}
	}, "DailySales.statement")

	// Loading: explicit and batch loading are criteria queries of the target (a many-to-many load: link, then target).
	other := newUser("Grace", "grace@example.com")
	if err := orm.Insert(ctx, db, other); err != nil {
		t.Fatal(err)
	}
	transaction := &sample.Transaction{UserID: user.ID, Status: sample.TransactionStatus("Pending"), Amount: orm.MustDecimal("5.00")}
	transaction.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, transaction); err != nil {
		t.Fatal(err)
	}
	role := &sample.Role{Name: "reader"}
	role.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, role); err != nil {
		t.Fatal(err)
	}
	link := &sample.UserRole{UserID: user.ID, RoleID: role.ID}
	link.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, link); err != nil {
		t.Fatal(err)
	}
	expectNames(t, sink, "Load", func() {
		if err := orm.Load(ctx, db, user, "Transactions"); err != nil {
			t.Fatal(err)
		}
	}, "Transaction.criteria")
	expectNames(t, sink, "LoadEach", func() {
		if err := orm.LoadEach(ctx, db, []*sample.User{user, other}, "Transactions"); err != nil {
			t.Fatal(err)
		}
	}, "Transaction.criteria")
	expectNames(t, sink, "a many-to-many Load", func() {
		if err := orm.Load(ctx, db, user, "Roles"); err != nil {
			t.Fatal(err)
		}
	}, "UserRole.criteria", "Role.criteria")

	// Eager loading: MultiQuery is the root and one query per navigation; Join is one `include` statement.
	expectNames(t, sink, "Include (multi-query)", func() {
		if _, err := orm.From[sample.Transaction](db).Include("User").List(ctx); err != nil {
			t.Fatal(err)
		}
	}, "Transaction.criteria", "User.criteria")
	expectNames(t, sink, "Include (join)", func() {
		if _, err := orm.From[sample.Transaction](db).Include("User").Fetch(orm.FetchJoin).List(ctx); err != nil {
			t.Fatal(err)
		}
	}, "Transaction.include")

	// Deletes: the key form, the entity form, and the soft delete (the flag-setting update) are all `delete`.
	soft := expectNames(t, sink, "soft delete", func() {
		if err := orm.DeleteEntity(ctx, db, role); err != nil {
			t.Fatal(err)
		}
	}, "Role.delete")
	if !strings.Contains(soft[0], "update") {
		t.Errorf("the soft delete is the flag-setting update, named for the operation asked for: %s", soft[0])
	}
	expectNames(t, sink, "Delete by key", func() {
		if err := orm.Delete[sample.User](ctx, db, other.ID); err != nil {
			t.Fatal(err)
		}
	}, "User.delete")
	expectNames(t, sink, "DeleteEntity", func() {
		if err := orm.DeleteEntity(ctx, db, transaction); err != nil {
			t.Fatal(err)
		}
	}, "Transaction.delete")
}

func TestStatementLog_AStatementRefusedBeforeItIsSentWritesNothing(t *testing.T) {
	ctx := context.Background()
	sink := &lineSink{}
	db := openLogged(t, sink, orm.Options{})
	sink.take()

	// CRUD-004: the navigation's key disagrees with the foreign key property. PRM-002: an args property the SQL never uses.
	mismatch := &sample.Transaction{UserID: 1, User: &sample.User{ID: 2}, Status: sample.TransactionStatus("Pending")}
	if err := orm.Insert(ctx, db, mismatch); orm.CodeOf(err) != "CRUD-004" {
		t.Fatalf("expected CRUD-004, got %v", err)
	}
	type unusedArgs struct{ Email, Extra string }
	unused := orm.Inline[unusedArgs, sample.User]("select id from users where email = @Email")
	if _, err := orm.Query(ctx, db, unused, unusedArgs{}); orm.CodeOf(err) != "PRM-002" {
		t.Fatalf("expected PRM-002, got %v", err)
	}
	if lines := sink.take(); len(lines) != 0 {
		t.Errorf("no SQL left the session, so nothing is logged: %v", lines)
	}
}

func TestStatementLog_ValuesAppearOnlyWhenAsked(t *testing.T) {
	ctx := context.Background()
	for _, values := range []bool{false, true} {
		sink := &lineSink{}
		db := openLogged(t, sink, orm.Options{LogParameterValues: values})
		user := newUser("Ada", "o'brien@example.com")
		sink.take()
		if err := orm.Insert(ctx, db, user); err != nil {
			t.Fatal(err)
		}
		insert := sink.take()[0]
		if _, err := orm.GetOrDefault[sample.User](ctx, db, int64(7)); err != nil {
			t.Fatal(err)
		}
		get := sink.take()[0]
		if _, err := orm.Query(ctx, db, sample.UsersByIDs, sample.UsersByIDsArgs{IDs: []int64{1, 2}}); err != nil {
			t.Fatal(err)
		}
		in := sink.take()[0]

		if !values {
			if !strings.HasSuffix(get, "[@k0]") || !strings.HasSuffix(in, "[@IDs_0, @IDs_1]") {
				t.Errorf("names only:\n%s\n%s", get, in)
			}
			if strings.Contains(insert, "o'brien") || strings.Contains(insert, "Ada") || strings.Contains(insert[strings.LastIndex(insert, "["):], " = ") {
				t.Errorf("a value reached the log without being asked: %s", insert)
			}
			continue
		}
		if !strings.HasSuffix(get, "[@k0 = 7]") {
			t.Errorf("an integer value: %s", get)
		}
		if !strings.HasSuffix(in, "[@IDs_0 = 1, @IDs_1 = 2]") {
			t.Errorf("an IN list expanded to its placeholders, each with its value: %s", in)
		}
		for _, want := range []string{"@email = 'o''brien@example.com'", "@display_name = null", "@name = 'Ada'"} {
			if !strings.Contains(insert, want) {
				t.Errorf("insert line lacks %s: %s", want, insert)
			}
		}
	}
}

func TestStatementLog_BytesAreHexOfTheFirstSixteenAndTheLength(t *testing.T) {
	ctx := context.Background()
	sink := &lineSink{}
	db := openLogged(t, sink, orm.Options{LogParameterValues: true})
	user := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	avatar := []byte{0xca, 0xfe, 0xba, 0xbe}
	profile := &sample.UserProfile{UserID: user.ID, Avatar: &avatar}
	profile.CreatedAtUtc = testsupport.SeedTime
	sink.take()
	if err := orm.Insert(ctx, db, profile); err != nil {
		t.Fatal(err)
	}
	if line := sink.take()[0]; !strings.Contains(line, "@avatar = 0xcafebabe (4 bytes)") {
		t.Errorf("bytes: %s", line)
	}
}

func TestStatementLog_UnsetWritesNothingAndTheSessionRunsAsBefore(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	user := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	if _, err := orm.Get[sample.User](ctx, db, user.ID); err != nil {
		t.Fatal(err)
	}
	// LogParameterValues alone is no log either.
	db = testsupport.OpenSampleWith(t, orm.Options{LogParameterValues: true})
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")
}

func TestStatementLog_AFlushAndAnExplicitBatchWriteOneLinePerStatementInOrderUnderOffAndAuto(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []orm.BatchingMode{orm.BatchingOff, orm.BatchingAuto} {
		sink := &lineSink{}
		db := openLogged(t, sink, orm.Options{Tracking: orm.TrackingTracked, Batching: mode, LogParameterValues: true})

		emails := []string{"a@example.com", "b@example.com", "c@example.com"}
		for _, email := range emails {
			if err := orm.Persist(db, newUser("u", email)); err != nil {
				t.Fatal(err)
			}
		}
		sink.take()
		if _, err := db.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		lines := sink.take()
		if !reflect.DeepEqual(logNames(lines), []string{"User.insert", "User.insert", "User.insert"}) {
			t.Fatalf("mode %v: a flush writes one line per statement: %v", mode, lines)
		}
		for i, email := range emails {
			if !strings.Contains(lines[i], "'"+email+"'") {
				t.Errorf("mode %v: statement %d is out of order: %s", mode, i, lines[i])
			}
		}

		// The explicit batch: every operation kind, in queue order.
		ada := testsupport.InsertUser(t, db, "Ada", "ada@x.example")
		grace := testsupport.InsertUser(t, db, "Grace", "grace@x.example")
		linus := newUser("Linus", "linus@x.example")
		sink.take()
		batch := db.Batch()
		for _, queued := range []error{
			orm.BatchInsert(batch, linus),
			orm.BatchUpdateOnly(batch, ada, "Name"),
			orm.BatchExecute(batch, sample.MarkTransactionStatus, markArgs),
			orm.BatchDelete[sample.User](batch, grace.ID),
		} {
			if queued != nil {
				t.Fatal(queued)
			}
		}
		if lines := sink.take(); len(lines) != 0 {
			t.Fatalf("queuing writes nothing: %v", lines)
		}
		if _, err := batch.Send(ctx); err != nil {
			t.Fatal(err)
		}
		if got := logNames(sink.take()); !reflect.DeepEqual(got, []string{"User.insert", "User.updateOnly", "command", "User.delete"}) {
			t.Errorf("mode %v: an explicit batch writes one line per operation in queue order: %v", mode, got)
		}
	}
}

func TestStatementLog_AOneRoundTripRealizationWritesTheMessagesLinesOnce(t *testing.T) {
	ctx := context.Background()
	sink := &lineSink{}
	dialect := testsupport.NewRecordingDialect()
	dialect.OneRoundTrip = true
	db := openLogged(t, sink, orm.Options{Dialect: dialect, Tracking: orm.TrackingTracked, Batching: orm.BatchingAuto})
	if !db.BatchingInEffect() {
		t.Fatal("the recording dialect behaves as a batching one")
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if err := orm.Persist(db, newUser("u", email)); err != nil {
			t.Fatal(err)
		}
	}
	sink.take()
	dialect.Reset()
	if _, err := db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	lines := sink.take()
	if !reflect.DeepEqual(logNames(lines), []string{"User.insert", "User.insert"}) {
		t.Errorf("the realization writes the message's lines, the session none of its own: %v", lines)
	}
	if len(dialect.Messages) == 0 {
		t.Error("the statements went through SendMessage")
	}
}

func TestStatementLog_ConsoleLogWritesTheLineToStandardOutput(t *testing.T) {
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = original }()

	// Opening the sample database creates its tables through the session: its first lines are the DDL.
	testsupport.OpenSampleWith(t, orm.Options{Dialect: sqlite.New(), Log: orm.ConsoleLog})
	writer.Close()
	os.Stdout = original
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "User.createTable: create table") {
		t.Errorf("the built-in sink writes each line to standard output, one per line: %q", output)
	}
}

func TestStatementLog_SchemaGuardWritesValidateLinesNamedForTheSource(t *testing.T) {
	ctx := context.Background()
	path, registry := migratedSampleDatabase(t)
	sink := &lineSink{}
	db, err := orm.Open(ctx, path, orm.Options{Dialect: sqlite.New(), Log: sink.write})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	registry.Entries = []orm.Entry{sample.UsersByEmail.Named("Queries.UsersByEmail"), sample.MarkTransactionStatus}
	registry.Entities = []reflect.Type{orm.TypeOf[sample.User]()}
	if err := orm.Validate(ctx, db, registry); err != nil {
		t.Fatal(err)
	}
	lines := sink.take()
	seen := map[string]bool{}
	for _, name := range logNames(lines) {
		seen[name] = true
	}
	for _, want := range []string{"validate Queries.UsersByEmail", "validate command", "validate User", "migrate"} {
		if !seen[want] {
			t.Errorf("no %q line among %v", want, seen)
		}
	}
	for name := range seen {
		if name != "migrate" && !strings.HasPrefix(name, "validate ") {
			t.Errorf("a line with an unexpected name: %s", name)
		}
	}
	for _, line := range lines {
		if strings.Contains(line, "begin") || strings.Contains(line, "savepoint") {
			t.Errorf("transaction control is not logged: %s", line)
		}
	}
}
