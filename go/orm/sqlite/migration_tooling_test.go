package sqlite_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// The migration-tooling members of the SQLite dialect (ADR-0043): the unstamped name, facets ignored, the view
// members, and the shadow host's temp file.

func TestName_IsSqlite(t *testing.T) {
	if got := sqlite.New().Name(); got != "sqlite" {
		t.Errorf("Name = %q, want sqlite", got)
	}
}

func TestStorageType_IgnoresTheFacets(t *testing.T) {
	d := sqlite.New()
	if got := d.StorageType(nil, &core.PropertyMap{ColumnType: core.TypeString, Length: 50}); got != "TEXT" {
		t.Errorf("a string with a Length facet: got %q, want TEXT", got)
	}
	if got := d.StorageType(nil, &core.PropertyMap{ColumnType: core.TypeDecimal, Precision: 18, Scale: 2}); got != "TEXT" {
		t.Errorf("a decimal with a Precision/Scale facet: got %q, want TEXT", got)
	}
}

func TestViewMembers_RenderThroughTheSeam(t *testing.T) {
	d := sqlite.New()
	if got, want := d.CreateViewFromBodySQL("totals", "select 1 as answer", false), "create view if not exists totals as\nselect 1 as answer"; got != want {
		t.Errorf("CreateViewFromBodySQL: got %q, want %q", got, want)
	}
	if got, want := d.DropViewSQL("totals", false), "drop view if exists totals"; got != want {
		t.Errorf("DropViewSQL: got %q, want %q", got, want)
	}
	if !d.ViewDefinitionIsVerbatim() {
		t.Error("sqlite_master keeps the view text as submitted: ViewDefinitionIsVerbatim should be true")
	}
}

func TestShadowHost_ProvisionsATempFileAndDropsIt(t *testing.T) {
	var host migrations.ShadowHost = sqlite.ShadowHost{}
	connection, drop, err := host.Provision(context.Background(), "ignored")
	if err != nil {
		t.Fatal(err)
	}
	file, ok := strings.CutPrefix(connection, "Data Source=")
	if !ok {
		t.Fatalf("connection %q is not a Data Source form", connection)
	}

	pool, err := sqlite.New().CreateConnection(connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(context.Background(), "create table t (id INTEGER PRIMARY KEY) STRICT"); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("the temp database should exist: %v", err)
	}

	if err := drop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the temp database should be gone after drop, stat error: %v", err)
	}
	if _, ok := host.(migrations.TriggerReadBack); !ok {
		t.Error("SQLite stores the trigger text as submitted: the host should read one back")
	}
}
