package metadata_test

import (
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// Happy-path metadata loading across the sample models (mirrors
// dotnet/tests/SimpleOrm.Tests/EntityMapLoaderTests.cs).

func columnNames(m *core.EntityMap) []string {
	names := make([]string, len(m.Properties))
	for i, p := range m.Properties {
		names[i] = p.ColumnName
	}
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func propertyByColumn(m *core.EntityMap, column string) *core.PropertyMap {
	for _, p := range m.Properties {
		if p.ColumnName == column {
			return p
		}
	}
	return nil
}

func TestUser_MapsTableGeneratedKeyAndInheritedAuditColumns(t *testing.T) {
	loader := metadata.NewLoader(nil)
	m, err := metadata.Load[sample.User](loader)
	if err != nil {
		t.Fatal(err)
	}

	if m.Kind != core.RelationTable {
		t.Errorf("Kind = %v, want Table", m.Kind)
	}
	if m.RelationName != "users" {
		t.Errorf("RelationName = %q, want users", m.RelationName)
	}
	if m.KeyStrategy != core.KeyDatabaseGenerated {
		t.Errorf("KeyStrategy = %v, want DatabaseGenerated", m.KeyStrategy)
	}
	if len(m.KeyProperties) != 1 || m.KeyProperties[0].ColumnName != "id" {
		t.Errorf("KeyProperties = %+v", m.KeyProperties)
	}

	want := []string{"id", "name", "email", "display_name", "created_at", "updated_at"}
	if got := columnNames(m); !equalStrings(got, want) {
		t.Errorf("columns = %v, want %v (own fields first, base fields last)", got, want)
	}

	if propertyByColumn(m, "name").IsNullable {
		t.Error("name must not be nullable")
	}
	if !propertyByColumn(m, "updated_at").IsNullable {
		t.Error("updated_at must be nullable")
	}

	if len(m.Indexes) != 2 {
		t.Fatalf("Indexes = %+v, want 2", m.Indexes)
	}
	for _, idx := range m.Indexes {
		switch idx.Name {
		case "ix_users_email":
			if !idx.Unique {
				t.Error("ix_users_email must be unique")
			}
		case "ix_users_display_name":
			if idx.Unique {
				t.Error("ix_users_display_name must not be unique")
			}
		default:
			t.Errorf("unexpected index %q", idx.Name)
		}
	}
}

func TestUserRole_MapsCompositeNaturalKeyAndTwoRelationships(t *testing.T) {
	loader := metadata.NewLoader(nil)
	m, err := metadata.Load[sample.UserRole](loader)
	if err != nil {
		t.Fatal(err)
	}

	if m.KeyStrategy != core.KeyNatural {
		t.Errorf("KeyStrategy = %v, want Natural", m.KeyStrategy)
	}
	keyColumns := make([]string, len(m.KeyProperties))
	for i, k := range m.KeyProperties {
		keyColumns[i] = k.ColumnName
	}
	if !equalStrings(keyColumns, []string{"user_id", "role_id"}) {
		t.Errorf("key columns = %v", keyColumns)
	}
	if len(m.Relationships) != 2 {
		t.Fatalf("Relationships = %+v, want 2", m.Relationships)
	}
	for _, r := range m.Relationships {
		if r.PropertyName == "User" && r.TargetType != reflect.TypeFor[sample.User]() {
			t.Errorf("User relationship target = %v", r.TargetType)
		}
		if r.PropertyName == "Role" && !equalStrings(r.ForeignKeyProperties, []string{"RoleID"}) {
			t.Errorf("Role relationship FK = %v", r.ForeignKeyProperties)
		}
	}
}

func TestTransaction_MapsVersionIndexesAndForeignKey(t *testing.T) {
	loader := metadata.NewLoader(nil)
	m, err := metadata.Load[sample.Transaction](loader)
	if err != nil {
		t.Fatal(err)
	}

	if m.VersionProperty == nil || m.VersionProperty.ColumnName != "version" {
		t.Errorf("VersionProperty = %+v", m.VersionProperty)
	}
	if propertyByColumn(m, "user_id").ForeignKeyReferences != reflect.TypeFor[sample.User]() {
		t.Errorf("user_id.ForeignKeyReferences = %v", propertyByColumn(m, "user_id").ForeignKeyReferences)
	}

	if len(m.Indexes) != 2 {
		t.Fatalf("Indexes = %+v, want 2", m.Indexes)
	}
	var named *core.EntityIndex
	for _, idx := range m.Indexes {
		if idx.Name == "ix_transactions_status_created" {
			named = idx
		}
	}
	if named == nil {
		t.Fatal("ix_transactions_status_created not found")
	}
	if len(named.Columns) != 2 || named.Columns[0].Descending || !named.Columns[1].Descending {
		t.Errorf("named index columns = %+v", named.Columns)
	}

	var manyToOne *core.RelationshipMap
	for _, r := range m.Relationships {
		if r.Kind == core.RelationshipManyToOne {
			manyToOne = r
		}
	}
	if manyToOne == nil || !equalStrings(manyToOne.ForeignKeyProperties, []string{"UserID"}) {
		t.Errorf("many-to-one FK = %+v", manyToOne)
	}
}

func TestViewAndMaterializedView_MapWithTheirCapabilities(t *testing.T) {
	loader := metadata.NewLoader(nil)

	view, err := metadata.Load[sample.UserTransactionTotal](loader)
	if err != nil {
		t.Fatal(err)
	}
	if view.Kind != core.RelationView {
		t.Errorf("Kind = %v, want View", view.Kind)
	}
	if view.KeyStrategy != core.KeyNatural {
		t.Errorf("KeyStrategy = %v, want Natural", view.KeyStrategy)
	}
	if len(view.Indexes) != 0 {
		t.Errorf("view indexes = %+v, want none", view.Indexes)
	}

	materialized, err := metadata.Load[sample.MonthlySalesTotal](loader)
	if err != nil {
		t.Fatal(err)
	}
	if materialized.Kind != core.RelationMaterializedView {
		t.Errorf("Kind = %v, want MaterializedView", materialized.Kind)
	}
	if len(materialized.Indexes) != 1 || !materialized.Indexes[0].Unique {
		t.Errorf("materialized view indexes = %+v", materialized.Indexes)
	}
}

func TestStatement_MapsSQLAndDeclaredParameters(t *testing.T) {
	loader := metadata.NewLoader(nil)
	m, err := metadata.Load[sample.DailySales](loader)
	if err != nil {
		t.Fatal(err)
	}

	if m.Kind != core.RelationStatement {
		t.Errorf("Kind = %v, want Statement", m.Kind)
	}
	if m.RelationName != "" {
		t.Errorf("RelationName = %q, want empty", m.RelationName)
	}
	if m.KeyStrategy != core.KeyNone {
		t.Errorf("KeyStrategy = %v, want None", m.KeyStrategy)
	}
	if len(m.StatementParameters) != 1 || m.StatementParameters[0].Name != "since" {
		t.Fatalf("StatementParameters = %+v", m.StatementParameters)
	}
	if m.StatementParameters[0].Type.Name() != "Time" {
		t.Errorf("parameter type = %v, want time.Time", m.StatementParameters[0].Type)
	}
}

func TestProcedure_MapsKeylessAndNullableColumns(t *testing.T) {
	loader := metadata.NewLoader(nil)
	m, err := metadata.Load[sample.UserActivityReport](loader)
	if err != nil {
		t.Fatal(err)
	}

	if m.Kind != core.RelationProcedure {
		t.Errorf("Kind = %v, want Procedure", m.Kind)
	}
	if m.RelationName != "user_activity_report" {
		t.Errorf("RelationName = %q", m.RelationName)
	}
	if m.KeyStrategy != core.KeyNone {
		t.Errorf("KeyStrategy = %v, want None", m.KeyStrategy)
	}
	if len(m.StatementParameters) != 1 || m.StatementParameters[0].Name != "since" {
		t.Fatalf("StatementParameters = %+v", m.StatementParameters)
	}
	if !propertyByColumn(m, "last_transaction_at_utc").IsNullable {
		t.Error("last_transaction_at_utc must be nullable")
	}
}

func TestLoader_MapsAreCachedPerLoader(t *testing.T) {
	loader := metadata.NewLoader(nil)
	a, err := metadata.Load[sample.User](loader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := metadata.Load[sample.User](loader)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("Load must return the same cached map for the same loader")
	}
}
