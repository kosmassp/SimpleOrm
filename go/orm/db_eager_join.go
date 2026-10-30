package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
)

// listWithJoins is join-mode eager loading (spec/loading.md "Join", ADR-0022
// add.1): one SELECT with a LEFT JOIN per included navigation (two for
// many-to-many — the unprojected link, then the projected target), built as
// core.SelectJoin entries on the root AST (spec/query-ast.md "Level 2
// extensions": root alias t, projected joins j0… in include order, link joins
// l<n>), rendered by the dialect, and read back by partitioning each row's
// columns into segments by their "<alias>_" prefix — the "t_" segment is the
// root, each "j<n>_" segment a navigation target — mapped through the one
// mapping pipeline (mapping.Plan.ReadValues). Roots deduplicate and children
// share instances by key identity (§7.4, by structural value equality — never
// a stringified token, spec/loading.md); collections order by target key
// value-wise; a one-to-one matched twice is REL-002.
//
// Refusals, before any SQL: an unknown navigation is REL-001; a collection
// include with limit/offset is REL-005; more than one collection include is
// REL-006; a keyless root or target (or an FK shape the loader could not
// validate at declaration) is REL-003.
func listWithJoins[T any](ctx context.Context, q *CriteriaQuery[T], m *core.EntityMap, ast *core.SelectAst, queryName string) ([]T, error) {
	navs, err := resolveJoinNavigations(q.db, m, q.includes)
	if err != nil {
		return nil, err
	}
	joins, err := buildJoins(m, navs, ast, queryName)
	if err != nil {
		return nil, err
	}
	ast.Joins = joins

	rows, err := renderAndRunSelect(ctx, q.db, ast, queryName)
	if err != nil {
		return nil, err
	}
	return readJoinedRows[T](ctx, q.db, m, navs, rows, queryName)
}

// joinNavigation is one resolved Include: the declared relationship, its
// loaded target (and link, for many-to-many) map, and the aliases the join
// renders under (spec/query-ast.md: "the reference eager loader names
// projected joins j0… in include order and link joins l<n>").
type joinNavigation struct {
	relationship *core.RelationshipMap
	targetMap    *core.EntityMap
	linkMap      *core.EntityMap // many-to-many only
	alias        string          // the projected join's alias, "j<n>"
	linkAlias    string          // many-to-many only, "l<n>"
	isCollection bool            // one-to-many / many-to-many
}

// resolveJoinNavigations resolves every Include against m's declared
// navigations — relationshipNamed/unknownNavigationError (db_eager.go) is the
// one navigation-by-name lookup and REL-001 error every loading engine uses —
// and loads each target's (and many-to-many link's) map, numbering aliases in
// include order.
func resolveJoinNavigations(db *Db, m *core.EntityMap, includes []string) ([]*joinNavigation, error) {
	navs := make([]*joinNavigation, 0, len(includes))
	jIndex, lIndex := 0, 0
	for _, name := range includes {
		rel := relationshipNamed(m, name)
		if rel == nil {
			return nil, unknownNavigationError(m, name)
		}
		targetMap, err := db.maps.Load(rel.TargetType)
		if err != nil {
			return nil, err
		}
		nav := &joinNavigation{relationship: rel, targetMap: targetMap}
		if rel.Kind == core.RelationshipOneToMany || rel.Kind == core.RelationshipManyToMany {
			nav.isCollection = true
		}
		nav.alias = fmt.Sprintf("j%d", jIndex)
		jIndex++
		if rel.Kind == core.RelationshipManyToMany {
			linkMap, err := db.maps.Load(rel.LinkType)
			if err != nil {
				return nil, err
			}
			nav.linkMap = linkMap
			nav.linkAlias = fmt.Sprintf("l%d", lIndex)
			lIndex++
		}
		navs = append(navs, nav)
	}
	return navs, nil
}

// buildJoins applies the eager-loading refusals (REL-005/006/003) and builds
// one or two core.SelectJoin entries per navigation, in include order.
func buildJoins(m *core.EntityMap, navs []*joinNavigation, ast *core.SelectAst, queryName string) ([]*core.SelectJoin, error) {
	collectionCount := 0
	for _, nav := range navs {
		if nav.isCollection {
			collectionCount++
		}
	}
	if collectionCount > 0 && (ast.Limit != nil || ast.Offset != nil) {
		return nil, core.NewError("REL-005", queryName,
			"join-mode eager loading of a collection navigation refuses limit/offset — the join multiplies root rows; "+
				"use MultiQuery or SubSelect (to-one includes page fine)")
	}
	if collectionCount > 1 {
		return nil, core.NewError("REL-006", queryName,
			"join-mode eager loading joins at most one collection navigation — a Cartesian product; "+
				"use MultiQuery or SubSelect for the rest")
	}
	if len(m.KeyProperties) == 0 {
		return nil, core.Errorf("REL-003", queryName,
			"%s has no declared key; join-mode eager loading needs identity to reshape rows — load it via MultiQuery", m.EntityName())
	}

	var joins []*core.SelectJoin
	for _, nav := range navs {
		if len(nav.targetMap.KeyProperties) == 0 {
			return nil, core.Errorf("REL-003", queryName,
				"navigation %q targets %s, which has no declared key; load it via MultiQuery", nav.relationship.PropertyName, nav.targetMap.EntityName())
		}

		switch nav.relationship.Kind {
		case core.RelationshipManyToOne:
			pairs, err := fkOnOwnerPairs(m, nav.targetMap, nav.relationship, queryName)
			if err != nil {
				return nil, err
			}
			joins = append(joins, &core.SelectJoin{Target: nav.targetMap, Alias: nav.alias, On: pairs, Project: true})

		case core.RelationshipOneToOne, core.RelationshipOneToMany:
			pairs, err := fkOnTargetPairs(m, nav.targetMap, nav.relationship, queryName)
			if err != nil {
				return nil, err
			}
			joins = append(joins, &core.SelectJoin{Target: nav.targetMap, Alias: nav.alias, On: pairs, Project: true})

		case core.RelationshipManyToMany:
			linkPairs, err := linkPairsToOwner(m, nav.linkMap, nav.relationship, queryName)
			if err != nil {
				return nil, err
			}
			joins = append(joins, &core.SelectJoin{Target: nav.linkMap, Alias: nav.linkAlias, On: linkPairs, Project: false})

			targetPairs, err := linkPairsToTarget(nav.linkMap, nav.targetMap, nav.relationship, queryName)
			if err != nil {
				return nil, err
			}
			joins = append(joins, &core.SelectJoin{
				Target: nav.targetMap, Alias: nav.alias, ParentAlias: nav.linkAlias, On: targetPairs, Project: true,
			})
		}
	}
	return joins, nil
}

// fkOnOwnerPairs is a many-to-one join: the FK lives on owner, in target's key
// order (core.RelationshipMap.ForeignKeyProperties doc). A property the loader
// could not validate at declaration (unmapped, or an arity the target's
// runtime key disagrees with) is REL-003 here, never QRY-006 from the
// renderer.
func fkOnOwnerPairs(owner, target *core.EntityMap, rel *core.RelationshipMap, queryName string) ([]core.JoinPair, error) {
	if len(rel.ForeignKeyProperties) != len(target.KeyProperties) {
		return nil, core.Errorf("REL-003", queryName,
			"navigation %q declares %d foreign-key property(ies) but %s has a %d-part key",
			rel.PropertyName, len(rel.ForeignKeyProperties), target.EntityName(), len(target.KeyProperties))
	}
	pairs := make([]core.JoinPair, len(rel.ForeignKeyProperties))
	for i, fk := range rel.ForeignKeyProperties {
		if owner.Property(fk) == nil {
			return nil, core.Errorf("REL-003", queryName,
				"navigation %q's foreign-key property %q is not a mapped column of %s", rel.PropertyName, fk, owner.EntityName())
		}
		pairs[i] = core.JoinPair{ParentProperty: fk, TargetProperty: target.KeyProperties[i].PropertyName}
	}
	return pairs, nil
}

// fkOnTargetPairs is a one-to-one / one-to-many join: the FK lives on target,
// in owner's key order. The target FK property is exactly what the loader
// could not check at declaration time (spec/loading.md "Shape errors"), so an
// unmapped name here is REL-003.
func fkOnTargetPairs(owner, target *core.EntityMap, rel *core.RelationshipMap, queryName string) ([]core.JoinPair, error) {
	if len(rel.ForeignKeyProperties) != len(owner.KeyProperties) {
		return nil, core.Errorf("REL-003", queryName,
			"navigation %q declares %d foreign-key property(ies) but %s has a %d-part key",
			rel.PropertyName, len(rel.ForeignKeyProperties), owner.EntityName(), len(owner.KeyProperties))
	}
	pairs := make([]core.JoinPair, len(owner.KeyProperties))
	for i, ownerKey := range owner.KeyProperties {
		fk := rel.ForeignKeyProperties[i]
		if target.Property(fk) == nil {
			return nil, core.Errorf("REL-003", queryName,
				"navigation %q's foreign-key property %q is not a mapped column of %s", rel.PropertyName, fk, target.EntityName())
		}
		pairs[i] = core.JoinPair{ParentProperty: ownerKey.PropertyName, TargetProperty: fk}
	}
	return pairs, nil
}

// linkPairsToOwner is the many-to-many link join's ON: the link's FK
// properties referencing owner, in owner's key order (spec/query-ast.md's
// UserRole example: [["Id","UserId"]]).
func linkPairsToOwner(owner, link *core.EntityMap, rel *core.RelationshipMap, queryName string) ([]core.JoinPair, error) {
	if len(rel.LinkForeignKeysToOwner) != len(owner.KeyProperties) {
		return nil, core.Errorf("REL-003", queryName,
			"navigation %q's link %s declares %d foreign key(s) to %s but its key has %d part(s)",
			rel.PropertyName, link.EntityName(), len(rel.LinkForeignKeysToOwner), owner.EntityName(), len(owner.KeyProperties))
	}
	pairs := make([]core.JoinPair, len(owner.KeyProperties))
	for i, ownerKey := range owner.KeyProperties {
		fk := rel.LinkForeignKeysToOwner[i]
		if link.Property(fk) == nil {
			return nil, core.Errorf("REL-003", queryName,
				"navigation %q's link foreign-key property %q is not a mapped column of %s", rel.PropertyName, fk, link.EntityName())
		}
		pairs[i] = core.JoinPair{ParentProperty: ownerKey.PropertyName, TargetProperty: fk}
	}
	return pairs, nil
}

// linkPairsToTarget is the many-to-many target join's ON, hanging off the
// link alias: the link's FK properties referencing target, in target's key
// order (spec/query-ast.md's example: [["RoleId","Id"]]).
func linkPairsToTarget(link, target *core.EntityMap, rel *core.RelationshipMap, queryName string) ([]core.JoinPair, error) {
	if len(rel.LinkForeignKeysToTarget) != len(target.KeyProperties) {
		return nil, core.Errorf("REL-003", queryName,
			"navigation %q's link %s declares %d foreign key(s) to %s but its key has %d part(s)",
			rel.PropertyName, link.EntityName(), len(rel.LinkForeignKeysToTarget), target.EntityName(), len(target.KeyProperties))
	}
	pairs := make([]core.JoinPair, len(target.KeyProperties))
	for i, targetKey := range target.KeyProperties {
		fk := rel.LinkForeignKeysToTarget[i]
		if link.Property(fk) == nil {
			return nil, core.Errorf("REL-003", queryName,
				"navigation %q's link foreign-key property %q is not a mapped column of %s", rel.PropertyName, fk, link.EntityName())
		}
		pairs[i] = core.JoinPair{ParentProperty: fk, TargetProperty: targetKey.PropertyName}
	}
	return pairs, nil
}

// navReadState is one navigation's per-query reading plan: which row columns
// are its segment (by position, found through its "<alias>_" prefix) and the
// mapping.Plan that turns that segment into a *Target (or Target, for a value
// slice) instance.
type navReadState struct {
	nav     *joinNavigation
	field   reflect.StructField
	indices []int
	plan    *mapping.Plan
	// types are the driver-reported column types of this segment, in plan order (VAL-020 by database type).
	types []*sql.ColumnType
	// keys reads the target's key from a row (ADR-0048 item 5).
	keys rowKeyReader
}

// rowKeyReader reads an entity's key for one segment of a joined row (ADR-0048 item 5). For a single builtin integer
// key (loading_keys.go) the key is an int64 read from the row itself — the segment column named as the key column,
// when the driver hands back an int64 — so a root repeated once per child row, or a child repeated across rows, is
// recognized before it is materialized again; any other key is the boxed tuple under the same structural comparison.
type rowKeyReader struct {
	integer  bool
	property *core.PropertyMap // the integer key property
	column   int               // the key column's position in the row, or -1 when the segment lacks it
}

func newRowKeyReader(m *core.EntityMap, indices []int, stripped []string) rowKeyReader {
	// A nullable key field keeps the tuple path, where a null part compares as it always did.
	if !integerKey(m.KeyProperties) || m.KeyProperties[0].Type.Kind() == reflect.Pointer {
		return rowKeyReader{}
	}
	reader := rowKeyReader{integer: true, property: m.KeyProperties[0], column: -1}
	for i, name := range stripped {
		if strings.EqualFold(name, reader.property.ColumnName) {
			reader.column = indices[i]
			break
		}
	}
	return reader
}

// fromRow is the key straight from the row, when the driver returned the key column as an int64.
func (r rowKeyReader) fromRow(raw []any) (int64, bool) {
	if !r.integer || r.column < 0 {
		return 0, false
	}
	key, ok := raw[r.column].(int64)
	return key, ok
}

// rowKey is one materialized instance's key: the int64 on the integer path, the tuple otherwise.
type rowKey struct {
	integer int64
	tuple   core.KeyTuple
}

func (r rowKeyReader) of(m *core.EntityMap, instance any) (rowKey, error) {
	if r.integer {
		key, ok := readIntegerKey(r.property, reflect.ValueOf(instance))
		if !ok {
			return rowKey{}, core.NewError("REL-003", m.EntityName(), "a joined row's key is null")
		}
		return rowKey{integer: key}, nil
	}
	values, err := m.KeyValues(instance)
	if err != nil {
		return rowKey{}, err
	}
	return rowKey{tuple: core.KeyTuple(values)}, nil
}

func (r rowKeyReader) equal(a, b rowKey) bool {
	if r.integer {
		return a.integer == b.integer
	}
	return a.tuple.Equal(b.tuple)
}

func (r rowKeyReader) compare(a, b rowKey) int {
	if r.integer {
		switch {
		case a.integer < b.integer:
			return -1
		case a.integer > b.integer:
			return 1
		}
		return 0
	}
	return a.tuple.Compare(b.tuple)
}

// ownerRecord is one deduplicated root instance (§7.4): the heap-allocated
// *T every row for this key writes into, its key (kept as values, never a
// stringified token, spec/loading.md), and the per-navigation state used while
// folding rows (a collection's accumulated entries, a singular navigation's
// already-assigned target key, for REL-002 detection), indexed by navigation.
type ownerRecord struct {
	ptr         reflect.Value // *T
	key         rowKey
	collections [][]collectionEntry
	singular    []singularState
}

type singularState struct {
	assigned bool
	key      rowKey
}

type collectionEntry struct {
	key   rowKey
	value reflect.Value
}

// targetInstance is one navigation's shared-by-key target (§7.4: "owners
// sharing a target … share the same instance").
type targetInstance struct {
	key   rowKey
	value reflect.Value
}

// instanceIndex finds a navigation's (or the roots') instances by key: a map on the integer path, the linear
// structural scan otherwise.
type instanceIndex[V any] struct {
	reader  rowKeyReader
	byInt   map[int64]V
	entries []indexedEntry[V]
}

type indexedEntry[V any] struct {
	key   rowKey
	value V
}

func newInstanceIndex[V any](reader rowKeyReader) *instanceIndex[V] {
	index := &instanceIndex[V]{reader: reader}
	if reader.integer {
		index.byInt = map[int64]V{}
	}
	return index
}

func (x *instanceIndex[V]) find(key rowKey) (V, bool) {
	if x.byInt != nil {
		v, ok := x.byInt[key.integer]
		return v, ok
	}
	for _, e := range x.entries {
		if e.key.tuple.Equal(key.tuple) {
			return e.value, true
		}
	}
	var zero V
	return zero, false
}

func (x *instanceIndex[V]) add(key rowKey, value V) {
	if x.byInt != nil {
		x.byInt[key.integer] = value
		return
	}
	x.entries = append(x.entries, indexedEntry[V]{key, value})
}

// readJoinedRows runs the one SELECT and reshapes its rows into []T: each row
// is scanned once, split into the root segment and one segment per navigation
// by column-name prefix, mapped through mapping.Plan.ReadValues, and folded
// by structural key equality (never a stringified token, per spec/loading.md)
// into deduplicated roots with shared, ordered navigation values. A root or a
// target whose key the row already shows as known is not materialized again.
func readJoinedRows[T any](ctx context.Context, db *Db, m *core.EntityMap, navs []*joinNavigation, rows *resultRows, queryName string) ([]T, error) {
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	columnTypes, err := rows.ColumnTypes() // the VAL-020 database-type rule (core.TemporalMarkers) judges each segment's slice
	if err != nil {
		return nil, err
	}

	// T is the entity struct or a pointer to it: the pointer shape is what a tracked
	// session hands out as its own instance (Level 3 M1, CODING-STANDARD §10).
	rootIsPointer := reflect.TypeFor[T]().Kind() == reflect.Pointer
	rootType := stripPointer(reflect.TypeFor[T]())
	trackedRoot := db.tracking(m)
	trackedTargets := make([]*core.EntityMap, len(navs))
	for i, nav := range navs {
		trackedTargets[i] = db.tracking(nav.targetMap)
	}
	rootIndices, rootColumns := segmentColumns(columns, "t_")
	rootTypes := pickTypes(columnTypes, rootIndices)
	rootPlan, err := db.mapper.Plan(reflect.PointerTo(rootType), rootColumns, queryName)
	if err != nil {
		return nil, err
	}
	rootKeys := newRowKeyReader(m, rootIndices, rootColumns)

	states := make([]*navReadState, len(navs))
	for i, nav := range navs {
		field, ok := rootType.FieldByName(nav.relationship.PropertyName)
		if !ok {
			return nil, core.Errorf("REL-003", queryName,
				"navigation %q has no field on %s", nav.relationship.PropertyName, m.EntityName())
		}

		planType := reflect.PointerTo(nav.targetMap.Type)
		if nav.isCollection {
			if field.Type.Kind() != reflect.Slice {
				return nil, core.Errorf("REL-003", queryName, "navigation %q must be a slice field", nav.relationship.PropertyName)
			}
			if field.Type.Elem().Kind() != reflect.Pointer {
				planType = nav.targetMap.Type
			}
		} else if field.Type.Kind() != reflect.Pointer {
			return nil, core.Errorf("REL-003", queryName, "navigation %q must be a pointer field", nav.relationship.PropertyName)
		}

		indices, strippedColumns := segmentColumns(columns, nav.alias+"_")
		plan, err := db.mapper.Plan(planType, strippedColumns, queryName)
		if err != nil {
			return nil, err
		}
		states[i] = &navReadState{nav: nav, field: field, indices: indices, plan: plan, types: pickTypes(columnTypes, indices),
			keys: newRowKeyReader(nav.targetMap, indices, strippedColumns)}
	}

	var order []*ownerRecord
	owners := newInstanceIndex[*ownerRecord](rootKeys)
	targetInstances := make([]*instanceIndex[*targetInstance], len(navs))
	for i := range navs {
		targetInstances[i] = newInstanceIndex[*targetInstance](states[i].keys)
	}

	raw := make([]any, len(columns))
	scanArgs := make([]any, len(columns))
	for i := range raw {
		scanArgs[i] = &raw[i]
	}

	rowNumber := 0
	for rows.Next() {
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		rowNumber++
		if rowNumber%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}

		var owner *ownerRecord
		if key, ok := rootKeys.fromRow(raw); ok {
			owner, _ = owners.find(rowKey{integer: key})
		}
		if owner == nil {
			rootValue, err := rootPlan.ReadValuesTyped(pick(raw, rootIndices), rootTypes)
			if err != nil {
				return nil, err
			}
			rootKey, err := rootKeys.of(m, rootValue)
			if err != nil {
				return nil, err
			}
			owner, _ = owners.find(rootKey)
			if owner == nil {
				// An include reassigns the navigations it names on a tracked root and applies none of the
				// row's column values; other navigations stay as they were (spec/session.md "Tracking").
				var ptr reflect.Value
				if rootIsPointer {
					resolved, err := db.resolveAny(trackedRoot, rootValue)
					if err != nil {
						return nil, err
					}
					ptr = reflect.ValueOf(resolved)
				} else {
					resolved, err := db.resolveAny(trackedRoot, reflect.ValueOf(rootValue).Elem().Interface())
					if err != nil {
						return nil, err
					}
					ptr = reflect.New(rootType)
					ptr.Elem().Set(reflect.ValueOf(resolved))
				}
				owner = &ownerRecord{
					ptr: ptr, key: rootKey,
					collections: make([][]collectionEntry, len(navs)),
					singular:    make([]singularState, len(navs)),
				}
				for i, nav := range navs {
					if nav.isCollection {
						// An included collection with no matching rows is still an
						// empty, non-nil slice — never left nil (spec/loading.md).
						field := owner.ptr.Elem().FieldByIndex(states[i].field.Index)
						field.Set(reflect.MakeSlice(field.Type(), 0, 0))
					}
				}
				owners.add(rootKey, owner)
				order = append(order, owner)
			}
		}

		for i, nav := range navs {
			state := states[i]
			if allNilAt(raw, state.indices) {
				// A many-to-one/one-to-one with no match, or a many-to-many
				// link row whose target no longer exists, joins to an all-NULL
				// segment: a dead link loads as null/absent, never an error.
				continue
			}
			var instance *targetInstance
			var targetKey rowKey
			if key, ok := state.keys.fromRow(raw); ok {
				targetKey = rowKey{integer: key}
				instance, _ = targetInstances[i].find(targetKey)
			}
			if instance == nil {
				targetValue, err := state.plan.ReadValuesTyped(pick(raw, state.indices), state.types)
				if err != nil {
					return nil, err
				}
				targetKey, err = state.keys.of(nav.targetMap, targetValue)
				if err != nil {
					return nil, err
				}
				instance, _ = targetInstances[i].find(targetKey)
				if instance == nil {
					resolved, err := db.resolveAny(trackedTargets[i], targetValue)
					if err != nil {
						return nil, err
					}
					instance = &targetInstance{key: targetKey, value: reflect.ValueOf(resolved)}
					targetInstances[i].add(targetKey, instance)
				}
			}

			if nav.isCollection {
				if !hasCollectionEntry(state.keys, owner.collections[i], targetKey) {
					owner.collections[i] = append(owner.collections[i], collectionEntry{key: targetKey, value: instance.value})
				}
				continue
			}

			if singular := &owner.singular[i]; singular.assigned {
				if !state.keys.equal(singular.key, targetKey) {
					return nil, core.Errorf("REL-002", queryName,
						"navigation %q matched more than one row for one %s — its target foreign key needs a unique index",
						nav.relationship.PropertyName, m.EntityName())
				}
				continue
			}
			owner.singular[i] = singularState{assigned: true, key: targetKey}
			owner.ptr.Elem().FieldByIndex(state.field.Index).Set(instance.value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	results := make([]T, len(order))
	for i, owner := range order {
		for navIndex, nav := range navs {
			if !nav.isCollection {
				continue
			}
			entries := owner.collections[navIndex]
			keys := states[navIndex].keys
			// Collections order by target key value-wise, never by a string
			// rendering (spec/loading.md) — core.KeyTuple.Compare, the
			// comparator every loading sort uses (an integer key compares as the
			// int64 it is). Keys are distinct within a collection.
			sort.Slice(entries, func(a, b int) bool { return keys.compare(entries[a].key, entries[b].key) < 0 })
			field := owner.ptr.Elem().FieldByIndex(states[navIndex].field.Index)
			slice := reflect.MakeSlice(field.Type(), len(entries), len(entries))
			for j, e := range entries {
				slice.Index(j).Set(e.value)
			}
			field.Set(slice)
		}
		if rootIsPointer {
			results[i] = owner.ptr.Interface().(T)
		} else {
			results[i] = owner.ptr.Elem().Interface().(T)
		}
	}
	return results, nil
}

// segmentColumns returns, in row order, the positions and stripped names of
// every column carrying prefix ("t_" for the root, "<alias>_" for a projected
// join) — spec/query-ast.md's re-aliasing is what makes this partition
// unambiguous without positional guessing.
func segmentColumns(columns []string, prefix string) ([]int, []string) {
	var indices []int
	var stripped []string
	for i, c := range columns {
		if strings.HasPrefix(c, prefix) {
			indices = append(indices, i)
			stripped = append(stripped, strings.TrimPrefix(c, prefix))
		}
	}
	return indices, stripped
}

// pick copies values at indices, preserving order — one row's segment.
func pick(values []any, indices []int) []any {
	out := make([]any, len(indices))
	for i, idx := range indices {
		out[i] = values[idx]
	}
	return out
}

// pickTypes is pick for the driver-reported column types.
func pickTypes(types []*sql.ColumnType, indices []int) []*sql.ColumnType {
	out := make([]*sql.ColumnType, len(indices))
	for i, idx := range indices {
		out[i] = types[idx]
	}
	return out
}

// allNilAt reports whether every value of a row's segment is NULL — a LEFT
// JOIN's no-match row, read back as a dead link / no navigation value, never
// an error.
func allNilAt(values []any, indices []int) bool {
	for _, i := range indices {
		if values[i] != nil {
			return false
		}
	}
	return true
}

// hasCollectionEntry scans one owner's collection for a key by §7.4
// structural equality (never a stringified token, which loses information for
// date and blob keys, spec/loading.md) — an int64 comparison on the integer
// path. A collection is per owner, so the scan stays small.
func hasCollectionEntry(keys rowKeyReader, entries []collectionEntry, key rowKey) bool {
	for i := range entries {
		if keys.equal(entries[i].key, key) {
			return true
		}
	}
	return false
}
