package core

import (
	"fmt"
	"strings"
	"testing"
)

func TestCanonicalJSON_MatchesSystemTextJsonLayout(t *testing.T) {
	document := JSONObject{}.
		Set("entity", "User").
		Set("nested", JSONObject{}.Set("kind", "table").Set("flag", true)).
		Set("list", []any{int64(1), "two", nil}).
		Set("empty", []any{}).
		Set("count", 2)
	expected := strings.Join([]string{
		`{`,
		`  "entity": "User",`,
		`  "nested": {`,
		`    "kind": "table",`,
		`    "flag": true`,
		`  },`,
		`  "list": [`,
		`    1,`,
		`    "two",`,
		`    null`,
		`  ],`,
		`  "empty": [],`,
		`  "count": 2`,
		`}`,
	}, "\n")
	if got := CanonicalJSON(document); got != expected {
		t.Errorf("layout differs:\n%s\nwant:\n%s", got, expected)
	}
}

// The STJ default escaper: `>` becomes an uppercase \u escape (the pinned
// daily_sales.json carries `created_at >= @since`), so do `'` and `"`.
func TestCanonicalJSON_EscapesLikeSystemTextJson(t *testing.T) {
	got := CanonicalJSON(`a >= b 'c' "d" & e + <f>` + "\n\t" + "é\U0001F600")
	esc := func(r rune) string { return fmt.Sprintf(`\u%04X`, r) }
	expected := `"a ` + esc('>') + `= b ` + esc('\'') + `c` + esc('\'') + ` ` + esc('"') + `d` + esc('"') +
		` ` + esc('&') + ` e ` + esc('+') + ` ` + esc('<') + `f` + esc('>') + `\n\t` + esc('é') +
		esc(0xD83D) + esc(0xDE00) + `"`
	if got != expected {
		t.Errorf("escaping:\n got %s\nwant %s", got, expected)
	}
}

func TestCanonicalJSON_WritesSlicesOfObjectsAsArrays(t *testing.T) {
	got := CanonicalJSON([]JSONObject{JSONObject{}.Set("a", 1)})
	expected := "[\n  {\n    \"a\": 1\n  }\n]"
	if got != expected {
		t.Errorf("slice of objects:\n got %s\nwant %s", got, expected)
	}
}
