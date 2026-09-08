package amendfixture_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// TestSet_Renders proves the fixture renders through the real declaration
// loader — the same guarantee a (future) amend command relies on.
func TestSet_Renders(t *testing.T) {
	set, err := amendfixture.Set()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(metadata.NewLoader(nil), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 2 {
		t.Fatalf("expected 2 rendered versions, got %d", len(rendered))
	}
	if got := rendered[0].Steps[0].Up[0].SQL; got != "create table if not exists amend_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT" {
		t.Errorf("V0001 create: got %q", got)
	}
	if got := rendered[1].Steps[0].Up[0].SQL; got != "alter table amend_widgets add column note TEXT" {
		t.Errorf("V0002 add note: got %q", got)
	}
}
