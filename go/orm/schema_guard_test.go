package orm_test

// Mirrors dotnet/tests/SimpleOrm.Tests/SchemaGuardTests.cs and
// php/tests/Validation/SchemaGuardTest.php: every rule gets a failing
// fixture, asserted by code (core.CodeOf/HasCode, never message text,
// CODING-STANDARD §7), plus a clean run on the fully migrated sample database.

import (
	"context"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	samplemigrations "github.com/kosmassp/SimpleOrm/go/orm/sample/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// migratedSampleDatabase migrates the sample registry's set into a fresh
// temp-file database and returns its path and the registry, for tests that
// need real migration history (the MIG-* checks and the clean-run test).
func migratedSampleDatabase(t *testing.T) (string, orm.Registry) {
	t.Helper()
	ctx := context.Background()

	registry, err := samplemigrations.App()
	if err != nil {
		t.Fatalf("sample registry: %v", err)
	}

	path := testsupport.TempDatabase(t)
	pool, err := sqlite.New().CreateConnection("Data Source=" + path)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	snapshots, err := orm.SnapshotsFromFS(registry.Snapshots)
	if err != nil {
		t.Fatalf("load snapshots: %v", err)
	}

	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), registry.Migrations, snapshots)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return path, registry
}

// execOnFile runs one statement directly against the database file, outside
// any session (used to tamper with schema_version for the drift/unknown fixtures).
func execOnFile(t *testing.T, path, statement string, args ...any) {
	t.Helper()
	pool, err := sqlite.New().CreateConnection("Data Source=" + path)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	if _, err := pool.ExecContext(context.Background(), statement, args...); err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

func TestValidate_CleanSampleDatabaseReturnsNil(t *testing.T) {
	path, registry := migratedSampleDatabase(t)
	db, err := orm.Open(context.Background(), "Data Source="+path, orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := orm.Validate(context.Background(), db, registry); err != nil {
		t.Fatalf("expected a clean validation, got: %v", err)
	}
}

func TestValidate_MIG030PendingMigrations(t *testing.T) {
	ctx := context.Background()
	registry, err := samplemigrations.App()
	if err != nil {
		t.Fatalf("sample registry: %v", err)
	}

	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	err = orm.Validate(ctx, db, registry)
	if !orm.HasCode(err, "MIG-030") {
		t.Fatalf("expected MIG-030, got: %v", err)
	}
}

func TestValidate_MIG010DriftedChecksum(t *testing.T) {
	path, registry := migratedSampleDatabase(t)
	execOnFile(t, path,
		"update schema_version set checksum = 'deadbeef' where version = (select min(version) from schema_version)")

	db, err := orm.Open(context.Background(), "Data Source="+path, orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	err = orm.Validate(context.Background(), db, registry)
	if !orm.HasCode(err, "MIG-010") {
		t.Fatalf("expected MIG-010, got: %v", err)
	}
}

func TestValidate_MIG011UnknownHistory(t *testing.T) {
	path, registry := migratedSampleDatabase(t)
	execOnFile(t, path,
		"insert into schema_version (version, object, description, checksum, applied_at, execution_ms) "+
			"values (9999, 'ghost_table', 'ghost', 'x', '2026-01-01T00:00:00Z', 0)")

	db, err := orm.Open(context.Background(), "Data Source="+path, orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	err = orm.Validate(context.Background(), db, registry)
	if !orm.HasCode(err, "MIG-011") {
		t.Fatalf("expected MIG-011, got: %v", err)
	}
}

func TestValidate_PRM001UnknownPlaceholder(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.InlineCommand[sample.UsersByEmailArgs]("update users set name = name where email = @Email and 1 = @Bogus")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "PRM-001") {
		t.Fatalf("expected PRM-001, got: %v", err)
	}
}

func TestValidate_PRM002UnusedProperty(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.InlineCommand[sample.UsersByEmailArgs]("update users set name = name where 1 = 1")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "PRM-002") {
		t.Fatalf("expected PRM-002, got: %v", err)
	}
}

func TestValidate_VAL001StatementFailsToPrepare(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.InlineCommand[orm.EmptyArgs]("update no_such_table_at_all set x = 1")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "VAL-001") {
		t.Fatalf("expected VAL-001, got: %v", err)
	}
}

func TestValidate_VAL021SelectStar(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.InlineCommand[orm.EmptyArgs]("select * from users")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "VAL-021") {
		t.Fatalf("expected VAL-021, got: %v", err)
	}
}

func TestValidate_VAL020NonUTCTimestamp(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.InlineCommand[orm.EmptyArgs]("update users set updated_at = current_timestamp")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "VAL-020") {
		t.Fatalf("expected VAL-020, got: %v", err)
	}
}

// partialUserDTO binds id and name; a third selected column has no matching field (MAP-001).
type partialUserDTO struct {
	ID   int64
	Name string
}

func TestValidate_MAP001UnmatchedResultColumn(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.Inline[orm.EmptyArgs, partialUserDTO]("select id, name, 1 as extra from users limit 1")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "MAP-001") {
		t.Fatalf("expected MAP-001, got: %v", err)
	}
}

// needsExtraDTO requires a column the query does not select (MAP-002, the Go "required" rule).
type needsExtraDTO struct {
	ID    int64
	Extra int64
}

func TestValidate_MAP002MissingRequiredColumn(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.Inline[orm.EmptyArgs, needsExtraDTO]("select id from users limit 1")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "MAP-002") {
		t.Fatalf("expected MAP-002, got: %v", err)
	}
}

// nonNullableDisplayNameDTO maps the nullable users.display_name column to a
// non-pointer (non-nullable) field: VAL-010, the base-column case.
type nonNullableDisplayNameDTO struct {
	ID          int64
	DisplayName string
}

func TestValidate_VAL010NullableBaseColumn(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.Inline[orm.EmptyArgs, nonNullableDisplayNameDTO]("select id, display_name from users limit 1")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "VAL-010") {
		t.Fatalf("expected VAL-010, got: %v", err)
	}
}

// countDTO is bound to an aggregate expression column with no base column of its own.
type countDTO struct {
	Total int64
}

func TestValidate_VAL010ExpressionColumnUnknownNullability(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.Inline[orm.EmptyArgs, countDTO]("select count(*) as total from users")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "VAL-010") {
		t.Fatalf("expected VAL-010, got: %v", err)
	}
}

func TestValidate_VAL010ExpressionColumnNotNullOverridePasses(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.Inline[orm.EmptyArgs, countDTO]("select count(*) as total from users -- notnull: total")

	if err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil); err != nil {
		t.Fatalf("expected the -- notnull override to pass validation cleanly, got: %v", err)
	}
}

// badTypeDTO maps the TEXT, NOT NULL users.name column to an incompatible type.
type badTypeDTO struct {
	ID   int64
	Name int64
}

func TestValidate_VAL011IncompatibleDeclaredType(t *testing.T) {
	db := testsupport.OpenSample(t)
	entry := orm.Inline[orm.EmptyArgs, badTypeDTO]("select id, name from users limit 1")

	err := orm.ValidateTypes(context.Background(), db, []orm.Entry{entry}, nil)
	if !orm.HasCode(err, "VAL-011") {
		t.Fatalf("expected VAL-011, got: %v", err)
	}
}

// ghostEntity maps to a table that does not exist in the database (VAL-012).
type ghostEntity struct {
	ID int64 `orm:"column,key"`
}

func (ghostEntity) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("ghost_table_xyz")} }

func TestValidate_VAL012MissingRelation(t *testing.T) {
	db := testsupport.OpenSample(t)

	err := orm.ValidateTypes(context.Background(), db, nil, []reflect.Type{reflect.TypeFor[ghostEntity]()})
	if !orm.HasCode(err, "VAL-012") {
		t.Fatalf("expected VAL-012, got: %v", err)
	}
}

// usersWithBogusColumn maps an existing table but declares a column absent from it (VAL-013).
type usersWithBogusColumn struct {
	ID    int64  `orm:"column=id,key"`
	Bogus string `orm:"column=bogus_col_xyz"`
}

func (usersWithBogusColumn) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("users")} }

func TestValidate_VAL013MissingColumn(t *testing.T) {
	db := testsupport.OpenSample(t)

	err := orm.ValidateTypes(context.Background(), db, nil, []reflect.Type{reflect.TypeFor[usersWithBogusColumn]()})
	if !orm.HasCode(err, "VAL-013") {
		t.Fatalf("expected VAL-013, got: %v", err)
	}
}

// brokenEntity carries mapping declarations (the tagged ID) but leaves
// Untagged without an orm tag: a loader failure (MAP-010) that must surface
// as a ValidationError per violation, not abort the whole run.
type brokenEntity struct {
	ID       int64 `orm:"column,key"`
	Untagged string
}

func TestValidate_MappingErrorSurfaces(t *testing.T) {
	db := testsupport.OpenSample(t)

	err := orm.ValidateTypes(context.Background(), db, nil, []reflect.Type{reflect.TypeFor[brokenEntity]()})
	if !orm.HasCode(err, "MAP-010") {
		t.Fatalf("expected MAP-010, got: %v", err)
	}
}

func TestValidate_StatementEntityValidatesLikeAQuery(t *testing.T) {
	db := testsupport.OpenSample(t)

	err := orm.ValidateTypes(context.Background(), db, nil, []reflect.Type{reflect.TypeFor[sample.DailySales]()})
	if err != nil {
		t.Fatalf("expected the statement entity to validate cleanly, got: %v", err)
	}
}

func TestValidate_DormantKindsSkipped(t *testing.T) {
	db := testsupport.OpenSample(t)

	err := orm.ValidateTypes(context.Background(), db, nil, []reflect.Type{
		reflect.TypeFor[sample.MonthlySalesTotal](),  // materialized view: dormant on SQLite
		reflect.TypeFor[sample.UserActivityReport](), // procedure: dormant on SQLite
	})
	if err != nil {
		t.Fatalf("expected dormant kinds to be skipped, got: %v", err)
	}
}
