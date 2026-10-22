package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
)

// crudSQLCase is the conformance/crud-sql/*.json shape (ADR-0040 add.3,
// spec/crud.md, spec/dialects.md "Update and delete — the affected-row
// count"): one generated write of a fixture entity and its exact SQL text per
// dialect. Properties (update_only) are mapped property names in the order
// the session passes them; CheckVersion (delete) selects the version-checked
// form. The expect block is read as a map so every dialect astDialects knows
// is checked; a case lacking a shipped dialect's expectation fails.
type crudSQLCase struct {
	Name         string            `json:"name"`
	Entity       string            `json:"entity"`
	Statement    string            `json:"statement"`
	Properties   []string          `json:"properties"`
	CheckVersion bool              `json:"checkVersion"`
	Expect       map[string]string `json:"expect"`
}

// TestCrudSQL_RendersAsSpecified is the crud-sql/ runner (ADR-0040 add.3),
// mirroring ConformanceAstTests' structure: render each case's statement
// through every shipped dialect and compare the exact SQL text. Pure — no
// database.
func TestCrudSQL_RendersAsSpecified(t *testing.T) {
	const folder = "crud-sql"
	dir := testsupport.ConformanceFolder(t, folder)
	dialectNames := make([]string, 0, len(astDialects))
	for name := range astDialects {
		dialectNames = append(dialectNames, name)
	}
	sort.Strings(dialectNames)

	for _, name := range testsupport.ConformanceCases(t, folder) {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s/%s: %v", folder, name, err)
			}
			var c crudSQLCase
			if err := json.Unmarshal(data, &c); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			for _, dialectName := range dialectNames {
				t.Run(dialectName, func(t *testing.T) {
					renderCrudSQLCase(t, name, c, dialectName, astDialects[dialectName]())
				})
			}
		})
	}
}

// renderCrudSQLCase renders one case through one dialect and compares it with
// that dialect's pinned text.
func renderCrudSQLCase(t *testing.T, name string, c crudSQLCase, dialectName string, dialect core.Dialect) {
	t.Helper()

	m, err := resolveEntityMap(c.Entity)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	want, ok := c.Expect[dialectName]
	if !ok {
		t.Fatalf("%s lacks the %q expectation — every dialect pins every case (§9)", name, dialectName)
	}

	var got string
	switch c.Statement {
	case "insert":
		got = dialect.InsertSQL(m)
	case "update":
		got = dialect.UpdateSQL(m)
	case "update_only":
		properties := make([]*core.PropertyMap, len(c.Properties))
		for i, propertyName := range c.Properties {
			if properties[i] = m.Property(propertyName); properties[i] == nil {
				t.Fatalf("%s: %s has no mapped property %q", name, c.Entity, propertyName)
			}
		}
		got = dialect.UpdateOnlySQL(m, properties)
	case "delete":
		got = dialect.DeleteSQL(m, c.CheckVersion)
	default:
		t.Fatalf("%s: unknown statement %q", name, c.Statement)
	}
	if got != want {
		t.Errorf("sql:\n got  %q\n want %q", got, want)
	}
}
