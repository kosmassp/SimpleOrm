package orm

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// loadEach is the one loading engine (spec/loading.md): resolves the
// navigation (REL-001, REL-003), collects the owners' key/FK tuples
// (structural value equality, null parts excluded), issues the per-kind
// queries through the criteria pipeline ordered by target key value-wise, and
// attaches the results — overwriting the navigation with fresh state.
//
// ownerSubquery is nil for explicit/batch loading and for MultiQuery eager
// loading (key lists, chunked at core.LoadChunkSize); SubSelect eager loading
// passes the root query's AST, and the target filter becomes membership in
// (select <owner keys or FKs> from that query) — never chunked. The
// many-to-many link→target hop still key-lists.
func loadEach[T any](ctx context.Context, db *Db, entities []*T, navigation string, ownerSubquery *core.SelectAst) error {
	ownerMap, err := db.maps.Load(reflect.TypeFor[T]())
	if err != nil {
		return err
	}

	rel := relationshipNamed(ownerMap, navigation)
	if rel == nil {
		return unknownNavigationError(ownerMap, navigation)
	}

	targetMap, err := db.maps.Load(rel.TargetType)
	if err != nil {
		return err
	}

	switch rel.Kind {
	case core.RelationshipManyToOne:
		return loadManyToOne(ctx, db, ownerMap, targetMap, rel, entities, ownerSubquery)
	case core.RelationshipOneToOne:
		return loadOneToOne(ctx, db, ownerMap, targetMap, rel, entities, ownerSubquery)
	case core.RelationshipOneToMany:
		return loadOneToMany(ctx, db, ownerMap, targetMap, rel, entities, ownerSubquery)
	case core.RelationshipManyToMany:
		linkMap, err := db.maps.Load(rel.LinkType)
		if err != nil {
			return err
		}
		return loadManyToMany(ctx, db, ownerMap, targetMap, linkMap, rel, entities, ownerSubquery)
	default:
		return core.Errorf("REL-001", navigation, "%s is an unsupported relationship kind", rel.Kind)
	}
}

// eagerLoad fills the included navigations of the rows a criteria query just
// materialized (MultiQuery and SubSelect modes): one loadEach per navigation,
// over pointers into the caller's slice so the loaded state lands on the
// returned values. ownerSubquery is nil for MultiQuery (key-list loading) and
// the root AST for SubSelect; each navigation's loader takes its own copy
// before adding a Projection, so the same ast is safe to reuse across includes.
func eagerLoad[T any](ctx context.Context, db *Db, m *core.EntityMap, rows []T, includes []string, ownerSubquery *core.SelectAst, queryName string) error {
	_ = m
	_ = queryName
	pointers := make([]*T, len(rows))
	for i := range rows {
		pointers[i] = &rows[i]
	}
	for _, navigation := range includes {
		if err := loadEach(ctx, db, pointers, navigation, ownerSubquery); err != nil {
			return err
		}
	}
	return nil
}

// relationshipNamed finds a declared navigation by exact property name
// (spec/loading.md: "named by property name, exactly, case-sensitive").
func relationshipNamed(m *core.EntityMap, navigation string) *core.RelationshipMap {
	for _, r := range m.Relationships {
		if r.PropertyName == navigation {
			return r
		}
	}
	return nil
}

// unknownNavigationError is REL-001: the message lists the declared navigations.
func unknownNavigationError(m *core.EntityMap, navigation string) error {
	names := make([]string, len(m.Relationships))
	for i, r := range m.Relationships {
		names[i] = r.PropertyName
	}
	declared := "(none declared)"
	if len(names) > 0 {
		declared = strings.Join(names, ", ")
	}
	return core.Errorf("REL-001", m.EntityName()+"."+navigation,
		"is not a declared navigation of %s; declared: %s", m.EntityName(), declared)
}

// resolveProperties looks up mapped properties by name on m, in declaration
// order named by names. A name that exists as a Go field (declaration-time
// validation already checked that) but is not actually a mapped property of
// m's assembled EntityMap — ignored, or on a type only now fully loaded — is
// REL-003: a shape the loader could not validate at declaration time
// (spec/loading.md "Shape errors").
func resolveProperties(m *core.EntityMap, names []string, navigationTarget string) ([]*core.PropertyMap, error) {
	properties := make([]*core.PropertyMap, len(names))
	for i, name := range names {
		p := m.Property(name)
		if p == nil {
			return nil, core.Errorf("REL-003", navigationTarget,
				"'%s' is not a mapped property of %s", name, m.EntityName())
		}
		properties[i] = p
	}
	return properties, nil
}

// checkArity is REL-003's other shape: an FK property count that disagreed
// with a related key the declaration loader could not size (a target/link
// type with no static `key` tag it could scan — spec/loading.md: "an arity
// mismatch against a key the target never declared"). want == 0 means the
// related key is itself undeclared (keyless): nothing to compare.
func checkArity(navigationTarget string, have, want int, label string) error {
	if want > 0 && have != want {
		return core.Errorf("REL-003", navigationTarget,
			"declares %d %s but the related key has %d part(s)", have, label, want)
	}
	return nil
}

// navigationField is the exported struct field a relationship declaration
// names (relationship metadata carries only the property name; loading needs
// the field's index path, which FieldByName resolves through embedded structs
// the same way property mapping does).
func navigationField(entityType reflect.Type, propertyName string) reflect.StructField {
	field, _ := entityType.FieldByName(propertyName) // declaration-time validation guarantees this exists
	return field
}

// extractTuple reads properties from entity (a struct or pointer to one), in order.
func extractTuple(properties []*core.PropertyMap, entity any) core.KeyTuple {
	tuple := make(core.KeyTuple, len(properties))
	for i, p := range properties {
		tuple[i] = p.Get(entity)
	}
	return tuple
}

func propertyNames(properties []*core.PropertyMap) []string {
	names := make([]string, len(properties))
	for i, p := range properties {
		names[i] = p.PropertyName
	}
	return names
}

// distinctTuples de-duplicates by structural equality, preserving first-seen order.
func distinctTuples(tuples []core.KeyTuple) []core.KeyTuple {
	var result []core.KeyTuple
	for _, t := range tuples {
		found := false
		for _, existing := range result {
			if existing.Equal(t) {
				found = true
				break
			}
		}
		if !found {
			result = append(result, t)
		}
	}
	return result
}

// correlate builds each owner's tuple over properties (its own key, for
// one-to-one/one-to-many/many-to-many; its FK, for many-to-one), excluding a
// tuple with a null part from querying (spec/loading.md: "An owner whose key
// contains a null part is excluded from querying; its navigations stay
// empty/null"). owners[i] is nil for an excluded entity; distinct is the
// de-duplicated, non-null set the query filters on.
func correlate[T any](entities []*T, properties []*core.PropertyMap) (owners []core.KeyTuple, distinct []core.KeyTuple) {
	owners = make([]core.KeyTuple, len(entities))
	var nonNull []core.KeyTuple
	for i, e := range entities {
		t := extractTuple(properties, e)
		if t.HasNull() {
			continue
		}
		owners[i] = t
		nonNull = append(nonNull, t)
	}
	distinct = distinctTuples(nonNull)
	return owners, distinct
}

// chunkTuples splits tuples into groups of at most size (spec/loading.md,
// core.LoadChunkSize — "chunked only past the parameter budget").
func chunkTuples(tuples []core.KeyTuple, size int) [][]core.KeyTuple {
	if len(tuples) == 0 {
		return nil
	}
	var chunks [][]core.KeyTuple
	for i := 0; i < len(tuples); i += size {
		end := i + size
		if end > len(tuples) {
			end = len(tuples)
		}
		chunks = append(chunks, tuples[i:end])
	}
	return chunks
}

// membershipCriteria is the key-list filter (spec/loading.md: "composite keys
// as OR of ANDed equalities in key order"); a single-property key renders as
// a plain IN list instead — the same AST an InList criteria would build.
func membershipCriteria(propertyNames []string, tuples []core.KeyTuple) core.Criteria {
	if len(propertyNames) == 1 {
		values := make([]any, len(tuples))
		for i, t := range tuples {
			values[i] = t[0]
		}
		return &core.InList{Property: propertyNames[0], Values: values}
	}
	children := make([]core.Criteria, len(tuples))
	for i, t := range tuples {
		parts := make([]core.Criteria, len(propertyNames))
		for j, name := range propertyNames {
			parts[j] = core.Eq(name, t[j])
		}
		children[i] = core.And(parts...)
	}
	return core.Or(children...)
}

// selectRows renders and runs ast (whose Map is the entity actually being
// queried) through the dialect and the one row-mapping pipeline, without a
// compile-time type parameter: the loading engine only learns the target
// type at runtime, from the navigation's declaration. Results come back as
// addressable pointers (reflect.PointerTo(ast.Map.Type)) ready to attach
// directly to a pointer-shaped navigation field, or to copy into a value one.
func selectRows(ctx context.Context, db *Db, ast *core.SelectAst, queryName string) ([]reflect.Value, error) {
	var bound []any
	bind := func(value any, property *core.PropertyMap) (string, error) {
		name := fmt.Sprintf("c%d", len(bound))
		var columnType core.ColumnType
		if property != nil {
			columnType = property.ColumnType
		}
		converted, err := db.converter.ToDatabase(value, columnType, fmt.Sprintf("%s @%s", queryName, name))
		if err != nil {
			return "", err
		}
		bound = append(bound, sql.Named(name, converted))
		return "@" + name, nil
	}

	sqlText, err := db.options.Dialect.SelectSQL(ast, bind)
	if err != nil {
		return nil, err
	}

	rows, err := db.exec().QueryContext(ctx, sqlText, bound...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	plan, err := db.mapper.Plan(reflect.PointerTo(ast.Map.Type), columns, queryName)
	if err != nil {
		return nil, err
	}

	var results []reflect.Value
	count := 0
	for rows.Next() {
		value, err := plan.Read(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, reflect.ValueOf(value))
		count++
		if count%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// queryMembership is the one query-running path every per-kind loader uses:
// with ownerSubquery nil, a key-list query per chunk (merged and re-sorted,
// since each chunk is independently ordered by the database but chunks are
// not ordered relative to each other); with ownerSubquery non-nil (SubSelect
// eager loading), one unchunked query filtering by core.InSelect(matchProperties,
// the owner query projected to subqueryProjection) — never chunked
// (spec/loading.md "Eager loading" table). orderProperties is nil when the
// caller does not need database-side ordering (the many-to-many link hop).
func queryMembership(
	ctx context.Context, db *Db, m *core.EntityMap,
	matchProperties []string, tuples []core.KeyTuple,
	ownerSubquery *core.SelectAst, subqueryProjection []*core.PropertyMap,
	orderProperties []*core.PropertyMap, queryName string,
) ([]reflect.Value, error) {
	orderings := make([]core.Ordering, len(orderProperties))
	for i, p := range orderProperties {
		orderings[i] = core.Ordering{Property: p.PropertyName}
	}

	if ownerSubquery != nil {
		sub := *ownerSubquery // shallow copy: never mutate the shared root AST (CODING-STANDARD §3)
		sub.Projection = subqueryProjection
		ast := &core.SelectAst{Map: m, Where: []core.Criteria{core.InSelect(matchProperties, &sub)}, Orderings: orderings}
		return selectRows(ctx, db, ast, queryName)
	}

	if len(tuples) == 0 {
		return nil, nil
	}

	chunks := chunkTuples(tuples, core.LoadChunkSize)
	var all []reflect.Value
	for _, chunk := range chunks {
		ast := &core.SelectAst{Map: m, Where: []core.Criteria{membershipCriteria(matchProperties, chunk)}, Orderings: orderings}
		rows, err := selectRows(ctx, db, ast, queryName)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
	}
	if len(chunks) > 1 && len(orderProperties) > 0 {
		sortByProperties(all, orderProperties)
	}
	return all, nil
}

// sortByProperties re-establishes a single global order across merged chunks
// by comparing the ordering properties' actual values (never a stringified
// rendering — spec/loading.md, ADR-0021 add.1: "10 comes after 2").
func sortByProperties(values []reflect.Value, properties []*core.PropertyMap) {
	sort.SliceStable(values, func(i, j int) bool {
		left := extractTuple(properties, values[i].Interface())
		right := extractTuple(properties, values[j].Interface())
		return compareTuples(left, right) < 0
	})
}

func compareTuples(a, b core.KeyTuple) int {
	for i := range a {
		if c := compareValue(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// compareValue orders two values of the same underlying type (both always
// come from the same mapped property) — the fixed table types a key or FK
// column can realistically hold. Anything else falls back to comparing the
// formatted text, which is not truly value-wise; no fixture or sample key
// needs it today (see the final report's "Spec gaps").
func compareValue(a, b any) int {
	switch av := a.(type) {
	case nil:
		if b == nil {
			return 0
		}
		return -1
	case int64:
		bv := b.(int64)
		return compareOrdered(av, bv)
	case int32:
		bv := b.(int32)
		return compareOrdered(av, bv)
	case int16:
		bv := b.(int16)
		return compareOrdered(av, bv)
	case int:
		bv := b.(int)
		return compareOrdered(av, bv)
	case float64:
		bv := b.(float64)
		return compareOrdered(av, bv)
	case float32:
		bv := b.(float32)
		return compareOrdered(av, bv)
	case string:
		return strings.Compare(av, b.(string))
	case bool:
		bv := b.(bool)
		if av == bv {
			return 0
		}
		if !av {
			return -1
		}
		return 1
	case time.Time:
		bv := b.(time.Time)
		switch {
		case av.Before(bv):
			return -1
		case av.After(bv):
			return 1
		default:
			return 0
		}
	case core.GUID:
		bv := b.(core.GUID)
		return bytes.Compare(av[:], bv[:])
	default:
		if b == nil {
			return 1
		}
		return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
	}
}

type ordered interface {
	~int | ~int16 | ~int32 | ~int64 | ~float32 | ~float64
}

func compareOrdered[T ordered](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// findByTuple returns the first value whose properties equal tuple, or an
// invalid Value when none match (a dead link, or no row: both load as nil —
// spec/loading.md).
func findByTuple(values []reflect.Value, properties []*core.PropertyMap, tuple core.KeyTuple) reflect.Value {
	for _, v := range values {
		if extractTuple(properties, v.Interface()).Equal(tuple) {
			return v
		}
	}
	return reflect.Value{}
}

// setPointerField sets entity's navigation field (a pointer, per MAP-020: every
// singular navigation is a pointer to a struct) to value, or to nil when value
// is the zero Value (no match / dead link / null FK / excluded owner).
func setPointerField(entity any, field reflect.StructField, value reflect.Value) {
	target := reflect.ValueOf(entity).Elem().FieldByIndex(field.Index)
	if !value.IsValid() {
		target.Set(reflect.Zero(field.Type))
		return
	}
	target.Set(value)
}

func setSliceField(entity any, field reflect.StructField, slice reflect.Value) {
	reflect.ValueOf(entity).Elem().FieldByIndex(field.Index).Set(slice)
}

// appendTarget appends one materialized target (always a *Target reflect.Value
// from selectRows) to a collection navigation's slice, matching its declared
// element shape ([]T or []*T, MAP-020) — value slices copy the pointee.
func appendTarget(slice reflect.Value, target reflect.Value, pointerElem bool) reflect.Value {
	if pointerElem {
		return reflect.Append(slice, target)
	}
	return reflect.Append(slice, target.Elem())
}

// --- many-to-one -------------------------------------------------------

// loadManyToOne fills the single target for each owner's FK tuple: owners
// sharing a target share the same instance (map lookup by structural
// equality, never string tokens); an owner with any null FK part keeps a
// null navigation and binds nothing (spec/loading.md "Per kind").
func loadManyToOne[T any](
	ctx context.Context, db *Db, ownerMap, targetMap *core.EntityMap, rel *core.RelationshipMap,
	entities []*T, ownerSubquery *core.SelectAst,
) error {
	navigationTarget := ownerMap.EntityName() + "." + rel.PropertyName
	ownerFKProps, err := resolveProperties(ownerMap, rel.ForeignKeyProperties, navigationTarget)
	if err != nil {
		return err
	}
	if err := checkArity(navigationTarget, len(ownerFKProps), len(targetMap.KeyProperties), "foreign-key property/properties"); err != nil {
		return err
	}

	field := navigationField(ownerMap.Type, rel.PropertyName)
	owners, distinct := correlate(entities, ownerFKProps)

	var subqueryProjection []*core.PropertyMap
	if ownerSubquery != nil {
		subqueryProjection = ownerFKProps
	}

	queryName := navigationTarget + " (many-to-one)"
	targets, err := queryMembership(ctx, db, targetMap, propertyNames(targetMap.KeyProperties), distinct,
		ownerSubquery, subqueryProjection, targetMap.KeyProperties, queryName)
	if err != nil {
		return err
	}

	for i, e := range entities {
		if owners[i] == nil {
			setPointerField(e, field, reflect.Value{})
			continue
		}
		setPointerField(e, field, findByTuple(targets, targetMap.KeyProperties, owners[i]))
	}
	return nil
}

// --- one-to-one ----------------------------------------------------------

// loadOneToOne fills the single target whose FK equals the owner's key, or
// null; more than one matching row is REL-002 — the unique index on the
// target FK is what makes a 1:1, and drift is refused, never resolved by
// picking one (spec/loading.md).
func loadOneToOne[T any](
	ctx context.Context, db *Db, ownerMap, targetMap *core.EntityMap, rel *core.RelationshipMap,
	entities []*T, ownerSubquery *core.SelectAst,
) error {
	navigationTarget := ownerMap.EntityName() + "." + rel.PropertyName
	targetFKProps, err := resolveProperties(targetMap, rel.ForeignKeyProperties, navigationTarget)
	if err != nil {
		return err
	}

	field := navigationField(ownerMap.Type, rel.PropertyName)
	owners, distinct := correlate(entities, ownerMap.KeyProperties)

	var subqueryProjection []*core.PropertyMap
	if ownerSubquery != nil {
		subqueryProjection = ownerMap.KeyProperties
	}

	queryName := navigationTarget + " (one-to-one)"
	targets, err := queryMembership(ctx, db, targetMap, propertyNames(targetFKProps), distinct,
		ownerSubquery, subqueryProjection, targetMap.KeyProperties, queryName)
	if err != nil {
		return err
	}

	if err := refuseDuplicateOneToOne(targets, targetFKProps, navigationTarget); err != nil {
		return err
	}

	for i, e := range entities {
		if owners[i] == nil {
			setPointerField(e, field, reflect.Value{})
			continue
		}
		setPointerField(e, field, findByTuple(targets, targetFKProps, owners[i]))
	}
	return nil
}

// refuseDuplicateOneToOne is REL-002: two target rows sharing the same FK tuple.
func refuseDuplicateOneToOne(targets []reflect.Value, targetFKProps []*core.PropertyMap, navigationTarget string) error {
	var seen []core.KeyTuple
	for _, v := range targets {
		tuple := extractTuple(targetFKProps, v.Interface())
		for _, existing := range seen {
			if existing.Equal(tuple) {
				return core.Errorf("REL-002", navigationTarget,
					"more than one row matched foreign key %v; the target needs a unique index", []any(tuple))
			}
		}
		seen = append(seen, tuple)
	}
	return nil
}

// --- one-to-many -----------------------------------------------------------

// loadOneToMany fills a fresh list of the targets whose FK equals the owner's
// key — empty, never null, ordered by target key (spec/loading.md).
func loadOneToMany[T any](
	ctx context.Context, db *Db, ownerMap, targetMap *core.EntityMap, rel *core.RelationshipMap,
	entities []*T, ownerSubquery *core.SelectAst,
) error {
	navigationTarget := ownerMap.EntityName() + "." + rel.PropertyName
	targetFKProps, err := resolveProperties(targetMap, rel.ForeignKeyProperties, navigationTarget)
	if err != nil {
		return err
	}

	field := navigationField(ownerMap.Type, rel.PropertyName)
	owners, distinct := correlate(entities, ownerMap.KeyProperties)

	var subqueryProjection []*core.PropertyMap
	if ownerSubquery != nil {
		subqueryProjection = ownerMap.KeyProperties
	}

	queryName := navigationTarget + " (one-to-many)"
	targets, err := queryMembership(ctx, db, targetMap, propertyNames(targetFKProps), distinct,
		ownerSubquery, subqueryProjection, targetMap.KeyProperties, queryName)
	if err != nil {
		return err
	}

	pointerElem := field.Type.Elem().Kind() == reflect.Pointer

	for i, e := range entities {
		slice := reflect.MakeSlice(field.Type, 0, 0)
		if owners[i] != nil {
			for _, v := range targets { // already ordered by target key
				if extractTuple(targetFKProps, v.Interface()).Equal(owners[i]) {
					slice = appendTarget(slice, v, pointerElem)
				}
			}
		}
		setSliceField(e, field, slice)
	}
	return nil
}

// --- many-to-many ------------------------------------------------------

// loadManyToMany resolves the targets referenced by the declared link's rows
// for each owner: two queries (link rows, then targets), de-duplicated,
// ordered by target key value-wise; a link row whose target row does not
// exist contributes nothing (spec/loading.md).
func loadManyToMany[T any](
	ctx context.Context, db *Db, ownerMap, targetMap, linkMap *core.EntityMap, rel *core.RelationshipMap,
	entities []*T, ownerSubquery *core.SelectAst,
) error {
	navigationTarget := ownerMap.EntityName() + "." + rel.PropertyName
	linkOwnerProps, err := resolveProperties(linkMap, rel.LinkForeignKeysToOwner, navigationTarget)
	if err != nil {
		return err
	}
	linkTargetProps, err := resolveProperties(linkMap, rel.LinkForeignKeysToTarget, navigationTarget)
	if err != nil {
		return err
	}
	if err := checkArity(navigationTarget, len(linkTargetProps), len(targetMap.KeyProperties), "link foreign-key propert(ies) to the target"); err != nil {
		return err
	}

	field := navigationField(ownerMap.Type, rel.PropertyName)
	owners, distinctOwners := correlate(entities, ownerMap.KeyProperties)

	var subqueryProjection []*core.PropertyMap
	if ownerSubquery != nil {
		subqueryProjection = ownerMap.KeyProperties
	}

	// Hop 1: link rows for these owners. The full link entity — never a
	// partial projection — because the one mapping pipeline requires an
	// entity result to match its EntityMap exactly (MAP-001/002); the link
	// table is small, and no ordering is required of it (only the target
	// query orders by the target key, spec/loading.md).
	linkQueryName := navigationTarget + " (many-to-many link)"
	linkRows, err := queryMembership(ctx, db, linkMap, propertyNames(linkOwnerProps), distinctOwners,
		ownerSubquery, subqueryProjection, nil, linkQueryName)
	if err != nil {
		return err
	}

	groups, allTargetTuples := groupLinkRows(linkRows, linkOwnerProps, linkTargetProps)

	// Hop 2: the targets themselves — always a key list, never a subquery
	// (spec/loading.md: "the many-to-many link→target hop still key-lists"),
	// ordered by target key value-wise.
	targetQueryName := navigationTarget + " (many-to-many target)"
	targets, err := queryMembership(ctx, db, targetMap, propertyNames(targetMap.KeyProperties), allTargetTuples,
		nil, nil, targetMap.KeyProperties, targetQueryName)
	if err != nil {
		return err
	}

	pointerElem := field.Type.Elem().Kind() == reflect.Pointer

	for i, e := range entities {
		slice := reflect.MakeSlice(field.Type, 0, 0)
		if owners[i] != nil {
			if group := findLinkGroup(groups, owners[i]); group != nil {
				for _, v := range targets { // already ordered by target key
					tuple := extractTuple(targetMap.KeyProperties, v.Interface())
					if group.hasTarget(tuple) {
						slice = appendTarget(slice, v, pointerElem)
					}
				}
			}
		}
		setSliceField(e, field, slice)
	}
	return nil
}

// linkGroup is one owner's set of target tuples, gathered from the link rows
// that reference it (§7.4 structural equality; a link row with a null part on
// either side correlates nothing and is dropped, spec/loading.md's null-FK
// symmetry — dead link data, not an error).
type linkGroup struct {
	owner   core.KeyTuple
	targets []core.KeyTuple
}

func (g *linkGroup) hasTarget(tuple core.KeyTuple) bool {
	for _, t := range g.targets {
		if t.Equal(tuple) {
			return true
		}
	}
	return false
}

func findLinkGroup(groups []*linkGroup, owner core.KeyTuple) *linkGroup {
	for _, g := range groups {
		if g.owner.Equal(owner) {
			return g
		}
	}
	return nil
}

// groupLinkRows turns the materialized link rows into per-owner target-tuple
// sets and the overall distinct set of target tuples to fetch.
func groupLinkRows(linkRows []reflect.Value, linkOwnerProps, linkTargetProps []*core.PropertyMap) ([]*linkGroup, []core.KeyTuple) {
	var groups []*linkGroup
	var allTargets []core.KeyTuple
	for _, v := range linkRows {
		entity := v.Interface()
		ownerTuple := extractTuple(linkOwnerProps, entity)
		targetTuple := extractTuple(linkTargetProps, entity)
		if ownerTuple.HasNull() || targetTuple.HasNull() {
			continue
		}
		group := findLinkGroup(groups, ownerTuple)
		if group == nil {
			group = &linkGroup{owner: ownerTuple}
			groups = append(groups, group)
		}
		if !group.hasTarget(targetTuple) {
			group.targets = append(group.targets, targetTuple)
		}
		allTargets = append(allTargets, targetTuple)
	}
	return groups, distinctTuples(allTargets)
}
