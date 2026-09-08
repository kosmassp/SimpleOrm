package orm

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// --- key reads (ADR-0006, ADR-0012) -----------------------------------------

// GetOrDefault reads by key (a single value, or []any for composite keys in
// key order); a missing row returns nil. QRY-005 for statements/procedures.
func GetOrDefault[T any](ctx context.Context, db *Db, key any) (*T, error) {
	entityType := reflect.TypeFor[T]()
	m, err := db.maps.Load(entityType)
	if err != nil {
		return nil, err
	}
	if err := requireNamedRelation(m, entityType, "key reads need a named relation"); err != nil {
		return nil, err
	}

	keyValues, err := validateKey(m, key)
	if err != nil {
		return nil, err
	}

	dialect := db.options.Dialect
	predicates := make([]string, len(keyValues))
	args := make([]any, len(keyValues))
	for i, value := range keyValues {
		name := fmt.Sprintf("k%d", i)
		predicates[i] = dialect.QuoteIdentifier(m.KeyProperties[i].ColumnName) + " = @" + name
		converted, err := db.converter.ToDatabase(value, "", fmt.Sprintf("%s key[%d]", entityType.Name(), i))
		if err != nil {
			return nil, err
		}
		args[i] = sql.Named(name, converted)
	}

	columns := make([]string, len(m.Properties))
	for i, p := range m.Properties {
		columns[i] = dialect.QuoteIdentifier(p.ColumnName)
	}
	sqlText := "select " + strings.Join(columns, ", ") +
		" from " + dialect.QuoteIdentifier(m.RelationName) +
		" where " + strings.Join(predicates, " and ")

	rows, err := db.exec().QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	resultColumns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	plan, err := db.mapper.Plan(entityType, resultColumns, entityType.Name()+" get")
	if err != nil {
		return nil, err
	}

	var result *T
	for rows.Next() {
		if result != nil {
			return nil, core.Errorf("QRY-002", entityType.Name(), "the key matched more than one row")
		}
		value, err := plan.Read(rows)
		if err != nil {
			return nil, err
		}
		typed := value.(T)
		result = &typed
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Get reads by key; a missing row throws CRUD-001 naming the entity and key.
func Get[T any](ctx context.Context, db *Db, key any) (T, error) {
	var zero T
	result, err := GetOrDefault[T](ctx, db, key)
	if err != nil {
		return zero, err
	}
	if result == nil {
		return zero, core.Errorf("CRUD-001", reflect.TypeFor[T]().Name(), "no row with key (%s)", formatKey(key))
	}
	return *result, nil
}

// validateKey is ADR-0006: the key (a value, or []any in key order) must match
// the EntityMap key in arity, order, and type — safe integer widening allowed.
func validateKey(m *core.EntityMap, key any) ([]any, error) {
	target := m.EntityName()
	if len(m.KeyProperties) == 0 {
		return nil, core.NewError("CRUD-002", target, "the entity defines no key")
	}

	provided, ok := key.([]any)
	if !ok {
		provided = []any{key}
	}
	if len(provided) != len(m.KeyProperties) {
		return nil, core.Errorf("CRUD-002", target,
			"the key has %d part(s), %d value(s) were provided", len(m.KeyProperties), len(provided))
	}

	coerced := make([]any, len(provided))
	for i, value := range provided {
		c, err := coerceKeyPart(value, m.KeyProperties[i], target, i)
		if err != nil {
			return nil, err
		}
		coerced[i] = c
	}
	return coerced, nil
}

var integerKinds = map[reflect.Kind]bool{
	reflect.Int: true, reflect.Int8: true, reflect.Int16: true, reflect.Int32: true, reflect.Int64: true,
	reflect.Uint: true, reflect.Uint8: true, reflect.Uint16: true, reflect.Uint32: true, reflect.Uint64: true,
}

func isUnsignedKind(k reflect.Kind) bool {
	switch k {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

// coerceKeyPart validates one key part: exact type match, or safe integer
// widening across kinds (Get[Order](ctx, db, 7) against an int64 key) — an
// out-of-range value still refuses.
func coerceKeyPart(value any, keyProperty *core.PropertyMap, target string, position int) (any, error) {
	expected := keyProperty.ValueType()
	if value == nil {
		return nil, keyPartError(target, keyProperty, position, expected, nil)
	}
	actual := reflect.TypeOf(value)
	if actual == expected {
		return value, nil
	}
	if !integerKinds[expected.Kind()] || !integerKinds[actual.Kind()] {
		return nil, keyPartError(target, keyProperty, position, expected, actual)
	}

	rv := reflect.ValueOf(value)
	var signed int64
	if isUnsignedKind(actual.Kind()) {
		unsigned := rv.Uint()
		if unsigned > math.MaxInt64 {
			return nil, keyPartError(target, keyProperty, position, expected, actual)
		}
		signed = int64(unsigned)
	} else {
		signed = rv.Int()
	}

	result := reflect.New(expected).Elem()
	if isUnsignedKind(expected.Kind()) {
		if signed < 0 {
			return nil, keyPartError(target, keyProperty, position, expected, actual)
		}
		if result.OverflowUint(uint64(signed)) {
			return nil, keyPartError(target, keyProperty, position, expected, actual)
		}
		result.SetUint(uint64(signed))
	} else {
		if result.OverflowInt(signed) {
			return nil, keyPartError(target, keyProperty, position, expected, actual)
		}
		result.SetInt(signed)
	}
	return result.Interface(), nil
}

func keyPartError(target string, keyProperty *core.PropertyMap, position int, expected, actual reflect.Type) error {
	actualName := "nil"
	if actual != nil {
		actualName = actual.String()
	}
	return core.Errorf("CRUD-002", target,
		"key part %d (%s) expects %s, got %s", position, keyProperty.PropertyName, expected, actualName)
}

// --- generated DDL and select-all (ADR-0011) --------------------------------

// CreateTable creates T's table and declared indexes from its metadata
// (idempotent: IF NOT EXISTS) — a dev/test utility; versioned migrations stay
// the schema-evolution path. Non-table sources are DDL-001.
func CreateTable[T any](ctx context.Context, db *Db) error {
	entityType := reflect.TypeFor[T]()
	m, err := db.maps.Load(entityType)
	if err != nil {
		return err
	}
	if m.Kind != core.RelationTable {
		return core.Errorf("DDL-001", entityType.Name(), "is %s-backed; only tables can be created from metadata", m.Kind)
	}

	if _, err := db.exec().ExecContext(ctx, db.options.Dialect.CreateTableSQL(m)); err != nil {
		return err
	}
	for _, indexSQL := range db.options.Dialect.CreateIndexSQL(m) {
		if _, err := db.exec().ExecContext(ctx, indexSQL); err != nil {
			return err
		}
	}
	return nil
}

// CreateView creates a view (or materialized view, where the dialect supports
// them) from T's defining SQL. Other sources are DDL-001; an unsupported
// materialized view is DDL-002.
func CreateView[T any](ctx context.Context, db *Db) error {
	entityType := reflect.TypeFor[T]()
	m, err := db.maps.Load(entityType)
	if err != nil {
		return err
	}
	if m.Kind != core.RelationView && m.Kind != core.RelationMaterializedView {
		return core.Errorf("DDL-001", entityType.Name(), "is %s-backed; CreateView applies to views only", m.Kind)
	}
	if m.Kind == core.RelationMaterializedView && !db.options.Dialect.SupportsMaterializedViews() {
		return core.NewError("DDL-002", entityType.Name(), "the dialect has no materialized views (SQLite; Level 4 Postgres will)")
	}

	if _, err := db.exec().ExecContext(ctx, db.options.Dialect.CreateViewSQL(m)); err != nil {
		return err
	}
	for _, indexSQL := range db.options.Dialect.CreateIndexSQL(m) {
		if _, err := db.exec().ExecContext(ctx, indexSQL); err != nil {
			return err
		}
	}
	return nil
}

// QueryAll is the generated select-all (ADR-0011 add.): explicit column list,
// ordered by the key when one exists. QRY-005 for statements/procedures.
func QueryAll[T any](ctx context.Context, db *Db) ([]T, error) {
	entityType := reflect.TypeFor[T]()
	m, err := db.maps.Load(entityType)
	if err != nil {
		return nil, err
	}
	if err := requireNamedRelation(m, entityType,
		"select-all needs a named relation (statements execute via the statement API)"); err != nil {
		return nil, err
	}

	dialect := db.options.Dialect
	columns := make([]string, len(m.Properties))
	for i, p := range m.Properties {
		columns[i] = dialect.QuoteIdentifier(p.ColumnName)
	}
	sqlText := "select " + strings.Join(columns, ", ") + " from " + dialect.QuoteIdentifier(m.RelationName)
	if len(m.KeyProperties) > 0 {
		keyColumns := make([]string, len(m.KeyProperties))
		for i, k := range m.KeyProperties {
			keyColumns[i] = dialect.QuoteIdentifier(k.ColumnName)
		}
		sqlText += " order by " + strings.Join(keyColumns, ", ")
	}

	rows, err := db.exec().QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	return materializeRows[T](ctx, db, rows, entityType.Name()+" select-all")
}

// --- generated CRUD (§7.14) -------------------------------------------------

// Insert writes every mapped non-generated column (§7.14): a database-generated
// key is read back via RETURNING and written onto the entity; an empty
// client-GUID key is assigned first. A non-null many-to-one navigation whose
// key disagrees with its FK property is CRUD-004; read-only sources are CRUD-003.
func Insert[T any](ctx context.Context, db *Db, entity *T) error {
	entityType := reflect.TypeFor[T]()
	m, err := db.maps.Load(entityType)
	if err != nil {
		return err
	}
	if m.Kind != core.RelationTable {
		return core.Errorf("CRUD-003", entityType.Name(), "is %s-backed and read-only; writes need a table", m.Kind)
	}

	if err := checkNavigationConsistency(db, m, entity, nil); err != nil {
		return err
	}

	if m.KeyStrategy == core.KeyClientGuid {
		keyProperty := m.KeyProperties[0]
		if g, ok := keyProperty.Get(entity).(core.GUID); ok && g.IsZero() {
			if err := keyProperty.Set(entity, core.NewGUID()); err != nil {
				return err
			}
		}
	}

	args := make([]any, 0, len(m.Properties))
	for _, p := range m.Properties {
		if p.IsGenerated {
			continue
		}
		converted, err := db.converter.ToDatabase(p.Get(entity), p.ColumnType, entityType.Name()+"."+p.PropertyName)
		if err != nil {
			return err
		}
		args = append(args, sql.Named(p.ColumnName, converted))
	}

	sqlText := db.options.Dialect.InsertSQL(m)

	if m.KeyStrategy == core.KeyDatabaseGenerated {
		rows, err := db.exec().QueryContext(ctx, sqlText, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			return fmt.Errorf("insert into %s: RETURNING produced no row", m.RelationName)
		}
		var generated any
		if err := rows.Scan(&generated); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		keyProperty := m.KeyProperties[0]
		value, err := db.converter.FromDatabase(generated, keyProperty.Type, keyProperty.ColumnType,
			entityType.Name()+"."+keyProperty.PropertyName)
		if err != nil {
			return err
		}
		return keyProperty.Set(entity, value)
	}

	_, err = db.exec().ExecContext(ctx, sqlText, args...)
	return err
}

// requireWritableKeyed is the CRUD-003/CRUD-002 guard shared by Update and Delete.
func requireWritableKeyed[T any](db *Db) (*core.EntityMap, error) {
	entityType := reflect.TypeFor[T]()
	m, err := db.maps.Load(entityType)
	if err != nil {
		return nil, err
	}
	if m.Kind != core.RelationTable {
		return nil, core.Errorf("CRUD-003", entityType.Name(), "is %s-backed and read-only; writes need a table", m.Kind)
	}
	if len(m.KeyProperties) == 0 {
		return nil, core.NewError("CRUD-002", entityType.Name(), "the entity defines no key")
	}
	return m, nil
}

// Update writes every mapped non-key column by key (§7.15). With a version
// column: SET version = version + 1, WHERE also requires the entity's current
// version, zero rows is a *core.ConcurrencyError (CRUD-010), and the entity's
// in-memory version is bumped on success. Without one, zero rows is CRUD-001.
// For a narrower SET see UpdateOnly.
func Update[T any](ctx context.Context, db *Db, entity *T) error {
	m, err := requireWritableKeyed[T](db)
	if err != nil {
		return err
	}
	if err := checkNavigationConsistency(db, m, entity, nil); err != nil {
		return err
	}

	// Bind SET values, key values, and the current version for the WHERE;
	// database-generated non-key columns are never written.
	var bound []*core.PropertyMap
	for _, p := range m.Properties {
		if p.IsKey || p.IsVersion || !p.IsGenerated {
			bound = append(bound, p)
		}
	}
	return executeUpdate(ctx, db, reflect.TypeFor[T](), m, entity, db.options.Dialect.UpdateSQL(m), bound)
}

// UpdateOnly is the update by column list (ADR-0028): it writes only the named
// properties — the caller says what changed — with every other rule of Update
// intact: keyed WHERE, version bump and check (CRUD-010), CRUD-001 without a
// version column. Names are field names (the criteria vocabulary). An unmapped
// name is CRUD-005; a key, version, or generated property is CRUD-006; an empty
// or repeating list is CRUD-007. A separate function, not an option on Update:
// the spec names one operation per concept.
func UpdateOnly[T any](ctx context.Context, db *Db, entity *T, properties ...string) error {
	m, err := requireWritableKeyed[T](db)
	if err != nil {
		return err
	}
	set, err := resolveUpdateList(m, properties)
	if err != nil {
		return err
	}
	written := make(map[string]bool, len(set))
	for _, p := range set {
		written[p.PropertyName] = true
	}
	if err := checkNavigationConsistency(db, m, entity, written); err != nil {
		return err
	}

	bound := append(append([]*core.PropertyMap{}, set...), m.KeyProperties...)
	if version := m.VersionProperty; version != nil {
		bound = append(bound, version)
	}
	return executeUpdate(ctx, db, reflect.TypeFor[T](), m, entity, db.options.Dialect.UpdateOnlySQL(m, set), bound)
}

// resolveUpdateList validates an update-by-column-list (ADR-0028) and resolves
// it to property maps, in the caller's order.
func resolveUpdateList(m *core.EntityMap, properties []string) ([]*core.PropertyMap, error) {
	entityName := m.EntityName()
	if len(properties) == 0 {
		return nil, core.NewError("CRUD-007", entityName, "update by column list needs at least one property")
	}

	resolved := make([]*core.PropertyMap, 0, len(properties))
	seen := make(map[string]bool, len(properties))
	for _, propertyName := range properties {
		target := entityName + "." + propertyName
		property := m.Property(propertyName)
		if property == nil {
			if owned := ownedNavigation(m, propertyName); owned != nil {
				// An owned navigation's name stands for all its members (ADR-0030).
				for _, member := range owned.Members {
					if seen[member.PropertyName] {
						return nil, core.NewError("CRUD-007", entityName+"."+member.PropertyName, "is listed more than once")
					}
					seen[member.PropertyName] = true
					resolved = append(resolved, member)
				}
				continue
			}
		}
		switch {
		case property == nil:
			return nil, core.NewError("CRUD-005", target, "is not a mapped property; the list takes field names")
		case property.IsKey:
			return nil, core.NewError("CRUD-006", target, "is a key property; an update never writes the key")
		case property.IsVersion:
			return nil, core.NewError("CRUD-006", target, "is the version column; the database computes it")
		case property.IsGenerated:
			return nil, core.NewError("CRUD-006", target, "is database-generated and never written")
		case seen[propertyName]:
			return nil, core.NewError("CRUD-007", target, "is listed more than once")
		}
		seen[propertyName] = true
		resolved = append(resolved, property)
	}
	return resolved, nil
}

// ownedNavigation finds an owned navigation (ADR-0030) by its field name, or nil.
func ownedNavigation(m *core.EntityMap, name string) *core.OwnedMap {
	for _, owned := range m.OwnedTypes {
		if owned.PropertyName() == name {
			return owned
		}
	}
	return nil
}

// executeUpdate is the shared tail of both updates (§7.15–16): bind, execute,
// judge the row count, bump the version.
func executeUpdate(ctx context.Context, db *Db, entityType reflect.Type, m *core.EntityMap, entity any, sqlText string, bound []*core.PropertyMap) error {
	args := make([]any, 0, len(bound))
	for _, p := range bound {
		converted, err := db.converter.ToDatabase(p.Get(entity), p.ColumnType, entityType.Name()+"."+p.PropertyName)
		if err != nil {
			return err
		}
		args = append(args, sql.Named(p.ColumnName, converted))
	}

	result, err := db.exec().ExecContext(ctx, sqlText, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		if version := m.VersionProperty; version != nil {
			return &core.ConcurrencyError{
				Target:  entityType.Name(),
				Message: fmt.Sprintf("update affected no rows: version %v is stale or the row is gone", version.Get(entity)),
			}
		}
		return core.Errorf("CRUD-001", entityType.Name(), "update affected no rows: no row with key (%s)", formatKeyOf(m, entity))
	}

	if version := m.VersionProperty; version != nil {
		current, err := toInt64(version.Get(entity))
		if err != nil {
			return err
		}
		bumped, err := fromInt64(current+1, version.ValueType())
		if err != nil {
			return err
		}
		return version.Set(entity, bumped)
	}
	return nil
}

// Delete deletes by key (a value, or []any for composite keys); zero rows is CRUD-001.
func Delete[T any](ctx context.Context, db *Db, key any) error {
	entityType := reflect.TypeFor[T]()
	m, err := requireWritableKeyed[T](db)
	if err != nil {
		return err
	}
	keyValues, err := validateKey(m, key)
	if err != nil {
		return err
	}

	args := make([]any, len(keyValues))
	for i, value := range keyValues {
		converted, err := db.converter.ToDatabase(value, "", fmt.Sprintf("%s key[%d]", entityType.Name(), i))
		if err != nil {
			return err
		}
		args[i] = sql.Named(m.KeyProperties[i].ColumnName, converted)
	}

	result, err := db.exec().ExecContext(ctx, db.options.Dialect.DeleteSQL(m, false), args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return core.Errorf("CRUD-001", entityType.Name(), "delete affected no rows: no row with key (%s)", formatKey(key))
	}
	return nil
}

// DeleteEntity is the version-checked delete (§7.16): with a version column,
// zero rows is a *core.ConcurrencyError (CRUD-010); without one, zero rows is CRUD-001.
func DeleteEntity[T any](ctx context.Context, db *Db, entity *T) error {
	entityType := reflect.TypeFor[T]()
	m, err := requireWritableKeyed[T](db)
	if err != nil {
		return err
	}
	checkVersion := m.VersionProperty != nil

	args := make([]any, 0, len(m.KeyProperties)+1)
	for _, key := range m.KeyProperties {
		converted, err := db.converter.ToDatabase(key.Get(entity), key.ColumnType, entityType.Name()+"."+key.PropertyName)
		if err != nil {
			return err
		}
		args = append(args, sql.Named(key.ColumnName, converted))
	}
	if checkVersion {
		version := m.VersionProperty
		converted, err := db.converter.ToDatabase(version.Get(entity), version.ColumnType, entityType.Name()+"."+version.PropertyName)
		if err != nil {
			return err
		}
		args = append(args, sql.Named(version.ColumnName, converted))
	}

	result, err := db.exec().ExecContext(ctx, db.options.Dialect.DeleteSQL(m, checkVersion), args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		if checkVersion {
			return &core.ConcurrencyError{
				Target:  entityType.Name(),
				Message: "delete affected no rows: the version is stale or the row is gone",
			}
		}
		return core.Errorf("CRUD-001", entityType.Name(), "delete affected no rows: no row with key (%s)", formatKeyOf(m, entity))
	}
	return nil
}

// checkNavigationConsistency is ADR-0005 add.1: the FK property is what is
// written; a non-null many-to-one navigation must agree with it pairwise, in
// key order (composite-aware). Arity mismatches are loader errors, not
// write-time ones. An update by column list (ADR-0028) passes the written
// property names and checks only those FK parts; nil checks every part.
func checkNavigationConsistency(db *Db, m *core.EntityMap, entity any, written map[string]bool) error {
	entityValue := reflect.ValueOf(entity).Elem()
	for _, relationship := range m.Relationships {
		if relationship.Kind != core.RelationshipManyToOne {
			continue
		}
		field := entityValue.FieldByName(relationship.PropertyName)
		if !field.IsValid() || field.Kind() != reflect.Pointer || field.IsNil() {
			continue
		}
		navigation := field.Interface()

		targetMap, err := db.maps.Load(relationship.TargetType)
		if err != nil {
			return err
		}
		if len(targetMap.KeyProperties) != len(relationship.ForeignKeyProperties) {
			continue // arity problems are loader errors, not write-time ones
		}

		for i, targetKey := range targetMap.KeyProperties {
			if written != nil && !written[relationship.ForeignKeyProperties[i]] {
				continue
			}
			navigationKey := targetKey.Get(navigation)
			fkProperty := m.Property(relationship.ForeignKeyProperties[i])
			foreignKey := fkProperty.Get(entity)
			if !reflect.DeepEqual(navigationKey, foreignKey) {
				return core.Errorf("CRUD-004", m.EntityName()+"."+relationship.PropertyName,
					"navigation key %v disagrees with %s = %v", navigationKey, relationship.ForeignKeyProperties[i], foreignKey)
			}
		}
	}
	return nil
}

func toInt64(value any) (int64, error) {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(v.Uint()), nil
	}
	return 0, fmt.Errorf("version property is not an integer type: %s", v.Type())
}

func fromInt64(value int64, target reflect.Type) (any, error) {
	result := reflect.New(target).Elem()
	switch target.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		result.SetInt(value)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		result.SetUint(uint64(value))
	default:
		return nil, fmt.Errorf("version property is not an integer type: %s", target)
	}
	return result.Interface(), nil
}

// From starts a criteria query over T (ADR-0012). The QRY-005 gate for
// statement/procedure sources lives at the session in the reference
// (Db.Query<T>()); Go's chain cannot return an error from From, so it fires at
// the terminal instead — still in the session, before any rendering
// (CODING-STANDARD §10 clarification, ADR-0027).
func From[T any](db *Db) *CriteriaQuery[T] {
	return &CriteriaQuery[T]{db: db}
}
