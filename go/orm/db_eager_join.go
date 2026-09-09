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
}

// ownerRecord is one deduplicated root instance (§7.4): the heap-allocated
// *T every row for this key writes into, its key (a core.KeyTuple — kept as
// values, not a stringified token, spec/loading.md), and the per-navigation
// state used while folding rows (a collection's accumulated entries, a
// singular navigation's already-assigned target key, for REL-002 detection).
type ownerRecord struct {
	ptr          reflect.Value // *T
	key          core.KeyTuple
	collections  map[int][]collectionEntry
	singularKeys map[int]core.KeyTuple
}

type collectionEntry struct {
	key   core.KeyTuple
	value reflect.Value
}

// targetInstance is one navigation's shared-by-key target (§7.4: "owners
// sharing a target … share the same instance").
type targetInstance struct {
	key   core.KeyTuple
	value reflect.Value
}

// readJoinedRows runs the one SELECT and reshapes its rows into []T: each row
// is scanned once, split into the root segment and one segment per navigation
// by column-name prefix, mapped through mapping.Plan.ReadValues, and folded
// by structural key equality (never a stringified token, per spec/loading.md)
// into deduplicated roots with shared, ordered navigation values.
func readJoinedRows[T any](ctx context.Context, db *Db, m *core.EntityMap, navs []*joinNavigation, rows *sql.Rows, queryName string) ([]T, error) {
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	rootType := reflect.TypeFor[T]()
	rootIndices, rootColumns := segmentColumns(columns, "t_")
	rootPlan, err := db.mapper.Plan(rootType, rootColumns, queryName)
	if err != nil {
		return nil, err
	}

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
		states[i] = &navReadState{nav: nav, field: field, indices: indices, plan: plan}
	}

	var order []*ownerRecord
	targetInstances := make([][]*targetInstance, len(navs))

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

		rootValue, err := rootPlan.ReadValues(pick(raw, rootIndices))
		if err != nil {
			return nil, err
		}
		rootKeyValues, err := m.KeyValues(rootValue)
		if err != nil {
			return nil, err
		}
		rootKey := core.KeyTuple(rootKeyValues)

		owner := findOwner(order, rootKey)
		if owner == nil {
			ptr := reflect.New(rootType)
			ptr.Elem().Set(reflect.ValueOf(rootValue))
			owner = &ownerRecord{
				ptr: ptr, key: rootKey,
				collections:  map[int][]collectionEntry{},
				singularKeys: map[int]core.KeyTuple{},
			}
			for i, nav := range navs {
				if nav.isCollection {
					// An included collection with no matching rows is still an
					// empty, non-nil slice — never left nil (spec/loading.md).
					field := owner.ptr.Elem().FieldByIndex(states[i].field.Index)
					field.Set(reflect.MakeSlice(field.Type(), 0, 0))
				}
			}
			order = append(order, owner)
		}

		for i, nav := range navs {
			state := states[i]
			segment := pick(raw, state.indices)
			if allNil(segment) {
				// A many-to-one/one-to-one with no match, or a many-to-many
				// link row whose target no longer exists, joins to an all-NULL
				// segment: a dead link loads as null/absent, never an error.
				continue
			}
			targetValue, err := state.plan.ReadValues(segment)
			if err != nil {
				return nil, err
			}
			targetKeyValues, err := nav.targetMap.KeyValues(targetValue)
			if err != nil {
				return nil, err
			}
			targetKey := core.KeyTuple(targetKeyValues)

			instance := findTargetInstance(targetInstances[i], targetKey)
			if instance == nil {
				instance = &targetInstance{key: targetKey, value: reflect.ValueOf(targetValue)}
				targetInstances[i] = append(targetInstances[i], instance)
			}

			if nav.isCollection {
				if findCollectionEntry(owner.collections[i], targetKey) == nil {
					owner.collections[i] = append(owner.collections[i], collectionEntry{key: targetKey, value: instance.value})
				}
				continue
			}

			if existing, assigned := owner.singularKeys[i]; assigned {
				if !existing.Equal(targetKey) {
					return nil, core.Errorf("REL-002", queryName,
						"navigation %q matched more than one row for one %s — its target foreign key needs a unique index",
						nav.relationship.PropertyName, m.EntityName())
				}
				continue
			}
			owner.singularKeys[i] = targetKey
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
			// Collections order by target key value-wise, never by a string
			// rendering (spec/loading.md) — core.KeyTuple.Compare, the same
			// comparator db_eager.go's sortByProperties uses for merged chunks.
			sort.Slice(entries, func(a, b int) bool { return entries[a].key.Compare(entries[b].key) < 0 })
			field := owner.ptr.Elem().FieldByIndex(states[navIndex].field.Index)
			slice := reflect.MakeSlice(field.Type(), len(entries), len(entries))
			for j, e := range entries {
				slice.Index(j).Set(e.value)
			}
			field.Set(slice)
		}
		results[i] = owner.ptr.Elem().Interface().(T)
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

// allNil reports whether every value in a segment is NULL — a LEFT JOIN's
// no-match row, read back as a dead link / no navigation value, never an
// error.
func allNil(values []any) bool {
	for _, v := range values {
		if v != nil {
			return false
		}
	}
	return true
}

// findOwner, findTargetInstance, and findCollectionEntry all scan a small,
// per-query list for a core.KeyTuple match by §7.4 structural equality
// (core.KeyTuple.Equal — never a stringified token, which loses information
// for date and blob keys, spec/loading.md). A linear scan is the price: fine
// at fixture and page scale, the same scale every eager-loading round trip
// already targets.
func findOwner(order []*ownerRecord, key core.KeyTuple) *ownerRecord {
	for _, o := range order {
		if o.key.Equal(key) {
			return o
		}
	}
	return nil
}

func findTargetInstance(list []*targetInstance, key core.KeyTuple) *targetInstance {
	for _, t := range list {
		if t.key.Equal(key) {
			return t
		}
	}
	return nil
}

func findCollectionEntry(entries []collectionEntry, key core.KeyTuple) *collectionEntry {
	for i := range entries {
		if entries[i].key.Equal(key) {
			return &entries[i]
		}
	}
	return nil
}
