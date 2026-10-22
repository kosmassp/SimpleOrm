package core

import (
	"reflect"
	"strings"
	"testing"
)

func TestTriggerEvents_ListAndTokensFollowTheFixedOrder(t *testing.T) {
	events := EventDelete | EventInsert
	if got := events.Tokens(); !reflect.DeepEqual(got, []string{"insert", "delete"}) {
		t.Errorf("Tokens = %v, want [insert delete] whatever the order they were combined in", got)
	}
	if !events.Has(EventDelete) || events.Has(EventUpdate) || events.Has(0) {
		t.Errorf("Has is wrong for %v", events)
	}

	parsed, ok := ParseTriggerEvents([]string{"update", "delete"})
	if !ok || parsed != EventUpdate|EventDelete {
		t.Errorf("ParseTriggerEvents = %v, %v", parsed, ok)
	}
	if _, ok := ParseTriggerEvents([]string{"update", "update"}); ok {
		t.Error("a repeated event must not parse")
	}
	if _, ok := ParseTriggerEvents([]string{"truncate"}); ok {
		t.Error("an unknown event must not parse")
	}
}

func TestTriggerTiming_TokensRoundTripAndTheZeroValueIsNoTiming(t *testing.T) {
	for _, timing := range []TriggerTiming{TimingBefore, TimingAfter, TimingInsteadOf} {
		parsed, ok := ParseTriggerTiming(timing.Token())
		if !ok || parsed != timing {
			t.Errorf("%v does not round-trip through %q", timing, timing.Token())
		}
	}
	if TriggerTiming(0).Token() != "" {
		t.Error("the zero timing must have no token — a forgotten timing is MAP-029, never a default")
	}
}

func TestScanTriggerTokens_FindsTheThreeTokensAndTheirSpans(t *testing.T) {
	body := "insert into log select n.id, {now} from {new: id, name} n join {old:a} o on 1 = 1"
	tokens, problems := ScanTriggerTokens(body)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(tokens) != 3 {
		t.Fatalf("got %d tokens: %+v", len(tokens), tokens)
	}
	if tokens[0].Kind != TokenNow || tokens[1].Kind != TokenNew || tokens[2].Kind != TokenOld {
		t.Errorf("kinds: %+v", tokens)
	}
	if !reflect.DeepEqual(tokens[1].Columns, []string{"id", "name"}) || !reflect.DeepEqual(tokens[2].Columns, []string{"a"}) {
		t.Errorf("columns: %+v", tokens)
	}
	if body[tokens[1].Start:tokens[1].End] != "{new: id, name}" {
		t.Errorf("span of the new token: %q", body[tokens[1].Start:tokens[1].End])
	}
}

func TestScanTriggerTokens_ReportsEveryMalformedOrUnknownToken(t *testing.T) {
	cases := map[string]string{
		"an unknown token":               "select {nope} from x",
		"a row token without columns":    "select * from {new} n",
		"a row token with an empty list": "select * from {new: } n",
		"an empty column entry":          "select * from {old: a, } o",
		"{now} with a column list":       "select {now: a} from x",
		"a -- line comment":              "select 1 from x; -- note",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, problems := ScanTriggerTokens(body); len(problems) == 0 {
				t.Errorf("%q should be reported", body)
			}
		})
	}
}

// The reference's token rules (spec/metadata-model.md "Triggers" Clarifications): any spelling of a kind, a
// repeated column, and a lone brace are accepted; only a single-quoted literal hides braces.
func TestScanTriggerTokens_AcceptsWhatTheReferenceAccepts(t *testing.T) {
	for _, body := range []string{"select {NOW}, {old: a, a} from { NEW : id ,name } n", "select {now from x", "select 1 } from x"} {
		if _, problems := ScanTriggerTokens(body); len(problems) != 0 {
			t.Errorf("%q: %v", body, problems)
		}
	}
	if got := NormalizeTriggerBody("select  '{x}', n.id from { NEW : id ,name } n;"); got != "select '{x}', n.id from {new: id, name} n;" {
		t.Errorf("canonical spelling: %q", got)
	}
}

func TestScanTriggerTokens_IgnoresBracesInLiterals(t *testing.T) {
	body := "insert into log values ('{\"a\": 1}', '{new: x}', '--') ;"
	tokens, problems := ScanTriggerTokens(body)
	if len(tokens) != 0 || len(problems) != 0 {
		t.Errorf("braces and dashes in literals are text: %+v %v", tokens, problems)
	}
	if got := ExpandTriggerTokens(body, func(TriggerToken) string { return "X" }); got != body {
		t.Errorf("expansion must leave literals alone, got %q", got)
	}
	if tokens, _ := ScanTriggerTokens("select 1 /* {now} */;"); len(tokens) != 1 {
		t.Errorf("a brace in a block comment is a token, as in the reference: %+v", tokens)
	}
}

func TestExpandTriggerTokens_ReplacesEachTokenAndNothingElse(t *testing.T) {
	body := "a {now} b {new: id} c {old: x, y} d"
	got := ExpandTriggerTokens(body, func(token TriggerToken) string { return "<" + token.Canonical() + ">" })
	want := "a <{now}> b <{new: id}> c <{old: x, y}> d"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizeTriggerBody_CollapsesWhitespaceAndCanonicalizesTokens(t *testing.T) {
	body := "insert into log (a)\n   select n.id,  {now}\n from {new:id,name}   n;\n"
	want := "insert into log (a) select n.id, {now} from {new: id, name} n;"
	if got := NormalizeTriggerBody(body); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := NormalizeTriggerBody(want); got != want {
		t.Errorf("normalization is not idempotent: %q", got)
	}
	// A body with a malformed token keeps its collapsed text: MAP-029 reports it, the normalizer never fails.
	if got := NormalizeTriggerBody("select {nope}   x"); got != "select {nope} x" {
		t.Errorf("got %q", got)
	}
}

func TestTriggerDefinition_SameAsIgnoresLayoutOnly(t *testing.T) {
	base := TriggerDefinition{
		Name: "t", Timing: TimingAfter, Events: EventUpdate, UpdateOf: []string{"a"},
		SQL: "insert into l select n.id from {new: id} n;",
	}
	layout := base
	layout.SQL = "insert into l\n  select n.id\n  from {new:id} n;"
	if !base.SameAs(layout) {
		t.Error("a layout-only difference declares nothing")
	}

	for name, change := range map[string]func(*TriggerDefinition){
		"timing":   func(d *TriggerDefinition) { d.Timing = TimingBefore },
		"events":   func(d *TriggerDefinition) { d.Events |= EventDelete },
		"updateOf": func(d *TriggerDefinition) { d.UpdateOf = []string{"a", "b"} },
		"body":     func(d *TriggerDefinition) { d.SQL = strings.Replace(d.SQL, "n.id", "n.name", 1) },
		"name":     func(d *TriggerDefinition) { d.Name = "other" },
	} {
		changed := base
		change(&changed)
		if base.SameAs(changed) {
			t.Errorf("a different %s must not be the same declaration", name)
		}
	}
}

type declaredTrigger struct{}

func (declaredTrigger) Trigger() TriggerDef { return TriggerDef{Name: "x"} }

type pointerDeclaredTrigger struct{}

func (*pointerDeclaredTrigger) Trigger() TriggerDef { return TriggerDef{Name: "p"} }

type undeclared struct{}

func TestTriggerDeclarationOf_ReadsValueAndPointerReceiversAndRefusesTheRest(t *testing.T) {
	if d, ok := TriggerDeclarationOf(reflect.TypeFor[declaredTrigger]()); !ok || d.Name != "x" {
		t.Errorf("value receiver: %v, %v", d, ok)
	}
	if d, ok := TriggerDeclarationOf(reflect.TypeFor[*pointerDeclaredTrigger]()); !ok || d.Name != "p" {
		t.Errorf("pointer receiver: %v, %v", d, ok)
	}
	if IsTriggerType(reflect.TypeFor[undeclared]()) || IsTriggerType(reflect.TypeFor[int]()) {
		t.Error("a type without a Trigger method is no trigger class")
	}
}
