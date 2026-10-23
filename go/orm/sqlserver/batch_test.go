package sqlserver

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The multi-statement realization of a planned message (ADR-0044) is text-and-values work plus a reading order, so it
// is tested here without an engine; the live leg (live_batch_test.go) runs it against a server where one answers.

func TestRenameApart_RenamesEveryRealPlaceholderAndItsArgumentPerStatement(t *testing.T) {
	text, args, err := renameApart(3,
		"update [users] set [name] = @Name where [id] = @id and [name] <> @NAME -- @Name stays\nand note = '@Name stays too'",
		[]any{sql.Named("Name", "Ada"), sql.Named("id", int64(7))})
	if err != nil {
		t.Fatal(err)
	}
	want := "update [users] set [name] = @p3_name where [id] = @p3_id and [name] <> @p3_name -- @Name stays\nand note = '@Name stays too'"
	if text != want {
		t.Errorf("text:\n got  %s\n want %s", text, want)
	}
	if !reflect.DeepEqual(args, []any{sql.Named("p3_name", "Ada"), sql.Named("p3_id", int64(7))}) {
		t.Errorf("args = %v", args)
	}
}

func TestRenameApart_ARepeatedNameInDifferentSpellingsIsOneParameter(t *testing.T) {
	text, args, err := renameApart(0, "select @Id, @ID, @id", []any{sql.Named("Id", int64(1))})
	if err != nil {
		t.Fatal(err)
	}
	if text != "select @p0_id, @p0_id, @p0_id" || len(args) != 1 {
		t.Errorf("got %q with %d args", text, len(args))
	}
}

func TestRenameApart_APositionalArgumentIsRefused(t *testing.T) {
	if _, _, err := renameApart(0, "select @a", []any{int64(1)}); err == nil {
		t.Fatal("the session binds every value with sql.Named")
	}
}

func TestJoinMessage_JoinsTheRenamedStatementsByASemicolonAndALineBreakInOrderAndNamesApartTheSameParameter(t *testing.T) {
	statements := []core.MessageStatement{
		{SQL: "insert into [t] ([a]) values (@a); select cast(scope_identity() as bigint)", Args: []any{sql.Named("a", "x")}, Result: core.ResultKey},
		{SQL: "set nocount on; update [t] set [a] = @a where [id] = @id; select @@rowcount; set nocount off",
			Args: []any{sql.Named("a", "y"), sql.Named("id", int64(2))}, Result: core.ResultReportedCount},
		{SQL: "insert into [u] ([a]) values (@a)", Args: []any{sql.Named("a", "z")}, Result: core.ResultNone},
	}
	text, args, err := joinMessage(statements)
	if err != nil {
		t.Fatal(err)
	}
	want := "insert into [t] ([a]) values (@p0_a); select cast(scope_identity() as bigint);\n" +
		"set nocount on; update [t] set [a] = @p1_a where [id] = @p1_id; select @@rowcount; set nocount off;\n" +
		"insert into [u] ([a]) values (@p2_a)"
	if text != want {
		t.Errorf("text:\n got  %s\n want %s", text, want)
	}
	names := make([]string, len(args))
	for i, arg := range args {
		names[i] = arg.(sql.NamedArg).Name
	}
	if strings.Join(names, ",") != "p0_a,p1_a,p1_id,p2_a" {
		t.Errorf("the same column parameter is bound apart in each statement, got %v", names)
	}
}

// scriptedResults plays back the result sets a response holds — one entry per set, each the rows it returns — so the
// reader's order of operations (advance to the next set, then read) can be judged without a driver.
type scriptedResults struct {
	sets      [][]any
	err       error
	set, next int
	exhausted bool
}

// Next models go-mssqldb: it reads on until the current set ends, and learns the next set only by running into it.
func (s *scriptedResults) Next() bool {
	if s.next < len(s.sets[s.set]) {
		s.next++
		return true
	}
	s.exhausted = true
	return false
}

// NextResultSet advances only once the current set was read to its end (go-mssqldb knows no next columns before that).
func (s *scriptedResults) NextResultSet() bool {
	if !s.exhausted || s.set+1 >= len(s.sets) {
		return false
	}
	s.set++
	s.next = 0
	s.exhausted = false
	return true
}

func (s *scriptedResults) Scan(dest ...any) error {
	*(dest[0].(*any)) = s.sets[s.set][s.next-1]
	return nil
}

func (s *scriptedResults) Err() error { return s.err }

func TestReadScalars_ReadsOneResultSetPerStatementThatHasOneInStatementOrder(t *testing.T) {
	statements := []core.MessageStatement{
		{Result: core.ResultKey},           // the first set
		{Result: core.ResultNone},          // no set
		{Result: core.ResultReportedCount}, // the second set
		{Result: core.ResultKey},           // the third
	}
	results := make([]core.MessageResult, len(statements))
	response := &scriptedResults{sets: [][]any{{int64(41)}, {int32(1)}, {int64(42)}}}
	if err := readScalars(response, statements, results); err != nil {
		t.Fatal(err)
	}
	if results[0].Key != int64(41) || results[2].Affected != 1 || results[3].Key != int64(42) {
		t.Errorf("results = %+v", results)
	}
}

func TestReadScalars_AMissingResultSetOrRowIsAnErrorNeverAZero(t *testing.T) {
	statements := []core.MessageStatement{{Result: core.ResultKey}, {Result: core.ResultReportedCount}}

	short := &scriptedResults{sets: [][]any{{int64(1)}}}
	if err := readScalars(short, statements, make([]core.MessageResult, 2)); err == nil || !strings.Contains(err.Error(), "no result for statement 2") {
		t.Errorf("a response with one set for two results is an error, got %v", err)
	}

	empty := &scriptedResults{sets: [][]any{{int64(1)}, {}}}
	if err := readScalars(empty, statements, make([]core.MessageResult, 2)); err == nil || !strings.Contains(err.Error(), "returned no row") {
		t.Errorf("a result set with no row is an error, got %v", err)
	}

	null := &scriptedResults{sets: [][]any{{int64(1)}, {nil}}}
	if err := readScalars(null, statements, make([]core.MessageResult, 2)); err == nil {
		t.Error("a NULL reported count is a dialect bug, not a zero")
	}
}

func TestReadScalars_TheDriversErrorWinsOverAMissingRow(t *testing.T) {
	failure := errors.New("Violation of UNIQUE KEY constraint")
	response := &scriptedResults{sets: [][]any{{}}, err: failure}
	err := readScalars(response, []core.MessageStatement{{Result: core.ResultKey}}, make([]core.MessageResult, 1))
	if !errors.Is(err, failure) {
		t.Errorf("the error the driver raised is the one reported, got %v", err)
	}
}

func TestReadScalars_AnErrorAfterTheLastResultSurfacesWhenTheResponseIsDrained(t *testing.T) {
	failure := errors.New("Violation of UNIQUE KEY constraint")
	response := &scriptedResults{sets: [][]any{{int64(1)}}, err: failure}
	err := readScalars(response, []core.MessageStatement{{Result: core.ResultKey}}, make([]core.MessageResult, 1))
	if !errors.Is(err, failure) {
		t.Errorf("a later statement's error is not lost, got %v", err)
	}
}

func TestIntegerOf_WidensWhateverIntegerShapeTheDriverReturns(t *testing.T) {
	for _, value := range []any{int64(5), int32(5), int16(5), int(5), uint8(5), []byte("5"), "5"} {
		if got, err := integerOf(value); err != nil || got != 5 {
			t.Errorf("%T: got %d, %v", value, got, err)
		}
	}
	if _, err := integerOf(nil); err == nil {
		t.Error("NULL is not a count")
	}
	if _, err := integerOf(1.5); err == nil {
		t.Error("a float is not a count")
	}
}

func TestBatchingFlags(t *testing.T) {
	d := New()
	if !d.SupportsStatementBatching() || d.MaxParametersPerMessage() != 2098 {
		t.Errorf("SQL Server batches as one multi-statement text under a cap of 2098 parameters, got %v / %d",
			d.SupportsStatementBatching(), d.MaxParametersPerMessage())
	}
}

func TestJoinMessage_ALineCommentEndingAStatementCannotSwallowTheNext(t *testing.T) {
	text, _, err := joinMessage([]core.MessageStatement{
		{SQL: "insert into [t] ([a]) values (1) -- trailing", Result: core.ResultNone},
		{SQL: "insert into [t] ([a]) values (2)", Result: core.ResultNone},
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "insert into [t] ([a]) values (1) -- trailing;\ninsert into [t] ([a]) values (2)" {
		t.Errorf("the second statement starts on its own line, got %q", text)
	}
}
