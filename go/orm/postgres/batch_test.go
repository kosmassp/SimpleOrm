package postgres

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The pieces of the pgx realization that are pure text-and-values work (ADR-0044) are tested without a connection;
// live_batch_test.go runs the pipeline against the server.

func TestHasMultipleStatements(t *testing.T) {
	for sqlText, want := range map[string]bool{
		"update users set name = 'x'":                                    false,
		"update users set name = 'x';":                                   false,
		"update users set name = 'x'; -- done":                           false,
		"update users set name = 'x'; /* done */ ":                       false,
		"update users set name = 'x'; update users set name = 'y'":       true,
		"update users set name = 'x';\nupdate users set name = 'y';":     true,
		"update users set note = 'a;b' where id = 1":                     false,
		"update users set note = 'it''s; fine' where id = 1":             false,
		`update "odd;name" set note = 'x'`:                               false,
		"select 1 -- a; b\n":                                             false,
		"select 1 /* a; /* nested; */ b; */":                             false,
		"do $$ begin perform 1; perform 2; end $$":                       false,
		"do $body$ begin perform 1; end $body$; select 1":                true,
		"select $1; select 2":                                            true,
		"update t set a = 1; /* c */ ; ":                                 false,
		"insert into t values (1); insert into t values (2)":             true,
		"update users set display_name = 'a' where name = 'Ada'; select": true,
	} {
		if got := hasMultipleStatements(sqlText); got != want {
			t.Errorf("hasMultipleStatements(%q) = %v, want %v", sqlText, got, want)
		}
	}
}

func TestNamedValues_KeepsTheNamesTheRewriteArrangesBy(t *testing.T) {
	values := namedValues([]any{sql.Named("Id", int64(7)), sql.Named("Name", "Ada")})
	r := rewritePlaceholders("update users set name = @name where id = @id")
	arranged, err := r.arrange(values)
	if err != nil {
		t.Fatal(err)
	}
	got := []any{arranged[0].Value, arranged[1].Value}
	if !reflect.DeepEqual(got, []any{"Ada", int64(7)}) {
		t.Errorf("arranged in $n order: %v", got)
	}
}

func TestIntegerScalar_WidensTheIntegerWidthsPgxScans(t *testing.T) {
	for _, value := range []any{int16(3), int32(3), int(3), int64(3)} {
		if got := integerScalar(value); got != int64(3) {
			t.Errorf("%T widened to %T %v, want int64 3", value, got, got)
		}
	}
	if got := integerScalar("x"); got != "x" {
		t.Errorf("a non-integer passes through, got %v", got)
	}
}

func TestBatchingFlags(t *testing.T) {
	d := New()
	if !d.SupportsStatementBatching() || d.MaxParametersPerMessage() != 0 {
		t.Errorf("PostgreSQL batches through pgx under no parameter cap, got %v / %d",
			d.SupportsStatementBatching(), d.MaxParametersPerMessage())
	}
	var _ core.StatementBatcher = d
}
