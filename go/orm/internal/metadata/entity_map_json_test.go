package metadata_test

import (
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// Export renders the exact conformance JSON of spec/metadata-model.md; the
// byte-for-byte pins live in the conformance runner
// (orm/internal/conformance/entities_test.go). These tests check the writer's
// shape rules in isolation: field order, omissions, and escaping.

func TestExport_OmitsVersionWhenUnmapped(t *testing.T) {
	m, err := metadata.Load[sample.User](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	json := mustExport(t, m)
	if strings.Contains(json, `"version"`) {
		t.Errorf("User export must not carry a version field:\n%s", json)
	}
}

func TestExport_IncludesVersionWhenMapped(t *testing.T) {
	m, err := metadata.Load[sample.Transaction](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	json := mustExport(t, m)
	if !strings.Contains(json, `"version": "version"`) {
		t.Errorf("Transaction export must carry the version column:\n%s", json)
	}
}

func TestExport_OmitsIndexesAndRelationshipsWhenEmpty(t *testing.T) {
	m, err := metadata.Load[sample.DailySales](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	json := mustExport(t, m)
	if strings.Contains(json, `"indexes"`) {
		t.Errorf("keyless statement export must not carry indexes:\n%s", json)
	}
	if strings.Contains(json, `"relationships"`) {
		t.Errorf("statement export must not carry relationships:\n%s", json)
	}
}

func TestExport_KeyColumnAndGeneratedFlagsOnlyWhenTrue(t *testing.T) {
	m, err := metadata.Load[sample.User](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	json := mustExport(t, m)
	if !strings.Contains(json, `"key": true`) || !strings.Contains(json, `"generated": true`) {
		t.Errorf("id column must carry key and generated flags:\n%s", json)
	}
	if strings.Contains(json, `"key": false`) || strings.Contains(json, `"generated": false`) {
		t.Errorf("false flags must be omitted, not written as false:\n%s", json)
	}
}

func TestExport_NormalizesSQLWhitespace(t *testing.T) {
	m, err := metadata.Load[sample.UserTransactionTotal](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	json := mustExport(t, m)

	// CanonicalJSON always escapes an embedded double quote as a unicode
	// escape sequence, never a backslash-quote, so the next raw double quote
	// after the opening one closes the string.
	start := strings.Index(json, `"sql": "`) + len(`"sql": "`)
	end := strings.IndexByte(json[start:], '"') + start
	sql := json[start:end]

	if strings.ContainsAny(sql, "\n\r\t") {
		t.Errorf("exported SQL must be whitespace-normalized to one line: %q", sql)
	}
	if strings.Contains(sql, "  ") {
		t.Errorf("exported SQL must not carry repeated spaces: %q", sql)
	}
}

func TestExport_EscapesHTMLSensitiveCharactersAsUppercaseUnicode(t *testing.T) {
	m, err := metadata.Load[sample.DailySales](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	json := mustExport(t, m)
	if !strings.Contains(json, `\u003E`) {
		t.Errorf("the '>' in 'created_at >= @since' must escape as \\u003E:\n%s", json)
	}
	if strings.ContainsRune(json, '>') {
		t.Errorf("a literal '>' must never appear in the export:\n%s", json)
	}
}

func TestExport_NoTrailingNewline(t *testing.T) {
	m, err := metadata.Load[sample.User](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	json := mustExport(t, m)
	if strings.HasSuffix(json, "\n") {
		t.Error("Export must not end with a trailing newline")
	}
}

// mustExport renders m with a fresh loader for the related entities (ADR-0029).
func mustExport(t *testing.T, m *core.EntityMap) string {
	t.Helper()
	json, err := metadata.Export(m, metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	return json
}
