package core

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// panicsWhenFormatted is a value that fails the test if anything reads it: "nothing is formatted when unset".
type panicsWhenFormatted struct{}

func (panicsWhenFormatted) String() string { panic("formatted") }

func TestStatementLog_UnsetFormatsAndWritesNothing(t *testing.T) {
	var unset *StatementLog
	unset.Statement("User.insert", "insert", []any{sql.Named("a", panicsWhenFormatted{})}) // would panic if formatted
	if NewStatementLog(nil, true) != nil {
		t.Fatal("a nil sink is no log")
	}
	LogStatement(context.Background(), "select 1", sql.Named("a", panicsWhenFormatted{}))
}

func TestFormatStatementLine_NamesInBindOrderAndValuesOnlyWhenAsked(t *testing.T) {
	args := []any{sql.Named("Name", "Ada"), sql.Named("Id", int64(7))}
	if got, want := FormatStatementLine("User.update", "update users set name = @Name where id = @Id", args, false),
		"User.update: update users set name = @Name where id = @Id [@Name, @Id]"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got, want := FormatStatementLine("User.update", "update users set name = @Name where id = @Id", args, true),
		"User.update: update users set name = @Name where id = @Id [@Name = 'Ada', @Id = 7]"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := FormatStatementLine("migrate", "select 1", nil, true); got != "migrate: select 1" {
		t.Errorf("a statement without parameters has no bracket: %s", got)
	}
}

func TestFormatLogValue_IsFixedAndCultureNeutral(t *testing.T) {
	long := make([]byte, 20)
	for i := range long {
		long[i] = byte(i)
	}
	cases := []struct {
		value any
		want  string
	}{
		{nil, "null"},
		{"it's", "'it''s'"},
		{true, "true"},
		{false, "false"},
		{int64(-5), "-5"},
		{int32(7), "7"},
		{1234567.5, "1234567.5"},
		{float32(0.1), "0.1"},
		{[]byte{0xde, 0xad}, "0xdead (2 bytes)"},
		{long, "0x000102030405060708090a0b0c0d0e0f… (20 bytes)"},
		{[]byte(nil), "null"},
		{[]int64{1, 2, 3}, "[1, 2, 3]"},
		{[]string{"a", "b'c"}, "['a', 'b''c']"},
		{time.Date(2026, 8, 29, 1, 0, 0, 0, time.FixedZone("x", 3600)), "'2026-08-29T00:00:00.0000000Z'"},
		{sql.NullString{}, "null"},
		{sql.NullString{String: "x", Valid: true}, "'x'"},
	}
	for _, c := range cases {
		if got := FormatLogValue(c.value); got != c.want {
			t.Errorf("FormatLogValue(%#v) = %s, want %s", c.value, got, c.want)
		}
	}
}

func TestStatementLogContext_NameDefaultsYieldToAnExplicitOne(t *testing.T) {
	var lines []string
	ctx := WithStatementLog(context.Background(), NewStatementLog(func(l string) { lines = append(lines, l) }, false))
	LogStatement(WithDefaultStatementName(ctx, "migrate"), "a")
	LogStatement(WithDefaultStatementName(WithStatementName(ctx, "shadow"), "migrate"), "b")
	LogStatement(WithStatementName(WithDefaultStatementName(ctx, "migrate"), "validate User"), "c")
	if strings.Join(lines, "|") != "migrate: a|shadow: b|validate User: c" {
		t.Errorf("lines: %v", lines)
	}
}
