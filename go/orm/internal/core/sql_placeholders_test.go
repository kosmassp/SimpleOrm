package core

import (
	"reflect"
	"testing"
)

func TestFindPlaceholders_IgnoresLiteralsAndComments(t *testing.T) {
	sql := "select @a, '@notme', 'it''s @nor', -- @comment\n /* @block */ @b, @a from t where x = @C"
	got := FindPlaceholders(sql)
	want := []string{"a", "b", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindPlaceholders = %v, want %v", got, want)
	}
}

func TestPlaceholderOccurrences_ReportsRealSpansCaseInsensitively(t *testing.T) {
	sql := "where id in (@ids) or '@ids' = x or id in (@IDS)"
	spans := PlaceholderOccurrences(sql, "ids")
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2: %v", len(spans), spans)
	}
	for _, span := range spans {
		if sql[span.Index:span.Index+span.Length] != "@ids" && sql[span.Index:span.Index+span.Length] != "@IDS" {
			t.Errorf("span %v is %q", span, sql[span.Index:span.Index+span.Length])
		}
	}
	if spans[0].Index != 13 || spans[1].Index != 43 {
		t.Errorf("spans at %d and %d", spans[0].Index, spans[1].Index)
	}
}

func TestFindPlaceholders_UnterminatedBlockCommentMasksToEnd(t *testing.T) {
	if got := FindPlaceholders("select @a /* @b"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("got %v", got)
	}
	if got := FindPlaceholders("select @a /* @b *"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("got %v", got)
	}
}

func TestPlaceholders_ASystemFunctionIsNeverAPlaceholder(t *testing.T) {
	sql := "update t set a = @a where id = @id; select @@rowcount, @@identity, @@ROWCOUNT; select @b"
	if got, want := FindPlaceholders(sql), []string{"a", "id", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FindPlaceholders = %v, want %v", got, want)
	}
	for _, name := range []string{"rowcount", "identity"} {
		if spans := PlaceholderOccurrences(sql, name); len(spans) != 0 {
			t.Errorf("@@%s is a system function, got spans %v", name, spans)
		}
	}
	if spans := PlaceholderOccurrences(sql, "a"); len(spans) != 1 || sql[spans[0].Index:spans[0].Index+spans[0].Length] != "@a" {
		t.Errorf("@a is still found, got %v", spans)
	}
}
