package postgres

import (
	"database/sql/driver"
	"strings"
	"testing"
)

// The placeholder rewrite is pure text-and-values work, tested without a connection; the live
// leg exercises it end to end against the server.

func TestRewritePlaceholders_NumbersByFirstAppearanceAndSharesRepeatedNames(t *testing.T) {
	r := rewritePlaceholders("select * from t where b = @B and a = @a and c = @b")
	if want := "select * from t where b = $1 and a = $2 and c = $1"; r.sql != want {
		t.Errorf("got %q, want %q", r.sql, want)
	}
	if strings.Join(r.names, ",") != "b,a" {
		t.Errorf("names %v, want [b a]", r.names)
	}
}

func TestRewritePlaceholders_LeavesLiteralsAndCommentsAlone(t *testing.T) {
	r := rewritePlaceholders("select '@lit', 'it''s @lit' -- @comment\n, /* @block */ @real, \"col\" from t where x = @real")
	if want := "select '@lit', 'it''s @lit' -- @comment\n, /* @block */ $1, \"col\" from t where x = $1"; r.sql != want {
		t.Errorf("got %q, want %q", r.sql, want)
	}
	if len(r.names) != 1 || r.names[0] != "real" {
		t.Errorf("names %v, want [real]", r.names)
	}
}

func TestRewritePlaceholders_TheIntrospectionRelationParameter(t *testing.T) {
	r := rewritePlaceholders("select 1 where a.attrelid = to_regclass(@relation) and x::bigint > 0")
	if want := "select 1 where a.attrelid = to_regclass($1) and x::bigint > 0"; r.sql != want {
		t.Errorf("got %q, want %q", r.sql, want)
	}
}

func TestRewritePlaceholders_AStatementWithoutPlaceholdersIsUntouched(t *testing.T) {
	for _, sqlText := range []string{
		"create table t (a bigint)",
		"select $1, '@x' -- @y",
		"select a @> b from t",
	} {
		r := rewritePlaceholders(sqlText)
		if r.sql != sqlText || len(r.names) != 0 {
			t.Errorf("%q should pass through, got %q %v", sqlText, r.sql, r.names)
		}
	}
}

func TestArrange_ReordersNamedValuesToPositionsCaseInsensitively(t *testing.T) {
	r := rewritePlaceholders("select @Second, @first")
	arranged, err := r.arrange([]driver.NamedValue{
		{Name: "FIRST", Ordinal: 1, Value: int64(1)},
		{Name: "second", Ordinal: 2, Value: "two"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(arranged) != 2 || arranged[0].Value != "two" || arranged[0].Ordinal != 1 || arranged[1].Value != int64(1) || arranged[1].Ordinal != 2 {
		t.Errorf("got %+v", arranged)
	}
}

func TestArrange_IsStrictBothWays(t *testing.T) {
	r := rewritePlaceholders("select @a, @b")
	if _, err := r.arrange([]driver.NamedValue{{Name: "a", Ordinal: 1, Value: 1}}); err == nil || !strings.Contains(err.Error(), "@b") {
		t.Errorf("a placeholder without a value should be named in the error, got %v", err)
	}
	if _, err := r.arrange([]driver.NamedValue{{Name: "a", Value: 1}, {Name: "b", Value: 2}, {Name: "z", Value: 3}}); err == nil || !strings.Contains(err.Error(), "@z") {
		t.Errorf("an argument without a placeholder should be named in the error, got %v", err)
	}
	if _, err := r.arrange([]driver.NamedValue{{Ordinal: 1, Value: 1}}); err == nil {
		t.Error("a positional argument beside @name placeholders should be refused")
	}
}

func TestArrange_AStatementWithoutPlaceholdersPassesItsArgumentsThrough(t *testing.T) {
	args := []driver.NamedValue{{Ordinal: 1, Value: int64(5)}}
	arranged, err := rewritePlaceholders("select $1").arrange(args)
	if err != nil || len(arranged) != 1 || arranged[0].Value != int64(5) {
		t.Errorf("got %+v, %v", arranged, err)
	}
}
