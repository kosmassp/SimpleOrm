package metadata_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// The manual loader and the loader precedence (explicit → declaration →
// convention). Mirrors dotnet/tests/SimpleOrm.Tests/EntityMapBuilderTests.cs.

// legacyFixture deliberately carries no orm tags: the "can't or won't annotate" case.
type legacyFixture struct {
	ID          int64
	DisplayName *string
}

// personFixture is a plain unannotated type for the convention loader.
type personFixture struct {
	ID        int64
	FirstName *string
}

func TestBuilder_MapsOnlyDeclaredPropertiesWithExplicitNames(t *testing.T) {
	builder := metadata.NewEntityMapBuilder[legacyFixture]().ToTable("legacy_items")
	builder.Property("ID").Column("legacy_id").Key().Generated()
	builder.Property("DisplayName").Column("display_name")

	options := (&orm.MappingOptions{}).Register(builder)
	m, err := metadata.Load[legacyFixture](metadata.NewLoader(options))
	if err != nil {
		t.Fatal(err)
	}

	if m.RelationName != "legacy_items" {
		t.Errorf("RelationName = %q", m.RelationName)
	}
	if m.KeyStrategy != core.KeyDatabaseGenerated {
		t.Errorf("KeyStrategy = %v", m.KeyStrategy)
	}
	if !equalStrings(columnNames(m), []string{"legacy_id", "display_name"}) {
		t.Errorf("columns = %v", columnNames(m))
	}
}

func TestExplicitRegistration_WinsOverDeclarations(t *testing.T) {
	builder := metadata.NewEntityMapBuilder[sample.User]().ToTable("users_manual")
	builder.Property("ID").Key().Generated()
	builder.Property("Name")

	options := (&orm.MappingOptions{}).Register(builder)
	m, err := metadata.Load[sample.User](metadata.NewLoader(options))
	if err != nil {
		t.Fatal(err)
	}

	if m.RelationName != "users_manual" {
		t.Errorf("RelationName = %q", m.RelationName)
	}
	if len(m.Properties) != 2 {
		t.Errorf("Properties = %+v, want 2", m.Properties)
	}
}

func TestConventionLoader_MapsUnannotatedTypes(t *testing.T) {
	m, err := metadata.Load[personFixture](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}

	if m.Kind != core.RelationTable {
		t.Errorf("Kind = %v, want Table", m.Kind)
	}
	if m.RelationName != "person_fixture" {
		t.Errorf("RelationName = %q, want person_fixture", m.RelationName)
	}
	if m.KeyStrategy != core.KeyDatabaseGenerated {
		t.Errorf("KeyStrategy = %v, want DatabaseGenerated", m.KeyStrategy)
	}
	if !equalStrings(columnNames(m), []string{"id", "first_name"}) {
		t.Errorf("columns = %v", columnNames(m))
	}
	if !propertyByColumn(m, "first_name").IsNullable {
		t.Error("first_name must be nullable (pointer field)")
	}
}

func TestBuilder_FailuresCarryCodesToo(t *testing.T) {
	builder := metadata.NewEntityMapBuilder[legacyFixture]()
	builder.Property("ID").Column("same")
	builder.Property("DisplayName").Column("same").Key()

	options := (&orm.MappingOptions{}).Register(builder)
	_, err := metadata.Load[legacyFixture](metadata.NewLoader(options))
	if !core.HasCode(err, "MAP-018") {
		t.Fatalf("expected MAP-018, got %v", err)
	}
}

func TestBuilder_UnknownFieldNameIsMAP023(t *testing.T) {
	builder := metadata.NewEntityMapBuilder[legacyFixture]()
	builder.Property("NoSuchField").Column("x")

	options := (&orm.MappingOptions{}).Register(builder)
	_, err := metadata.Load[legacyFixture](metadata.NewLoader(options))
	if !core.HasCode(err, "MAP-023") {
		t.Fatalf("expected MAP-023, got %v", err)
	}
}
