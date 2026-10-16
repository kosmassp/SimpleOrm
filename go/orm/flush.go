package orm

import (
	"container/heap"
	"context"
	"reflect"
	"sort"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// Flush writes the unit of work (Level 3 M2, spec/unit-of-work.md "Order of
// writes"): the scheduled inserts, each after the scheduled parents it
// references and with its keys flowed; then every dirty tracked instance as a
// column-list update; then the scheduled deletes, children first. Every
// statement is the session's own generated CRUD — insertEntity, updateEntity,
// deleteEntity — so the identity gate (UOW-002), the version rules (CRUD-010)
// and CRUD-004 apply unchanged, and the identity map and snapshots move exactly
// as the immediate operations move them.
//
// Inside the session's transaction when one is active (left open); otherwise
// one the flush begins, commits after the last statement, and rolls back on
// failure so nothing applies. A failed flush keeps the schedule and puts the
// session's in-memory state (identity map, snapshots, versions, generated
// keys) back as it was before the flush; rows written before the failure inside
// a caller's transaction stay in it until the caller rolls back. UOW-003 on an
// untracked session; UOW-004 for a cycle, before any SQL.
func (db *Db) Flush(ctx context.Context) (FlushSummary, error) {
	if err := db.requireUnitOfWork("Flush"); err != nil {
		return FlushSummary{}, err
	}
	if err := ctx.Err(); err != nil {
		return FlushSummary{}, err
	}

	plan, err := db.planFlush()
	if err != nil {
		return FlushSummary{}, err
	}
	// An empty flush returns the zero summary and begins no transaction (spec/unit-of-work.md Clarifications, ADR-0036).
	if plan.empty() {
		db.schedule().clear()
		return FlushSummary{}, nil
	}

	var tx *Tx
	if db.tx == nil {
		if tx, err = db.Begin(ctx); err != nil {
			return FlushSummary{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }() // a no-op once committed; the failure path's rollback
	}

	// A failed flush undoes its in-memory effects (Clarifications): the journal puts keys, tracking, snapshots,
	// and versions back — in the flush's own transaction and in a caller's alike; rows written before the
	// failure inside a caller's transaction stay in it until the caller rolls back.
	journal := &flushJournal{}
	summary, err := db.executeFlush(ctx, plan, journal)
	if err == nil && tx != nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		journal.rollback()
		return FlushSummary{}, err
	}
	db.schedule().clear()
	return summary, nil
}

// flushPlan is the order of a flush, computed before any SQL: inserts and
// deletes in dependency order, the dirty instances in update order.
type flushPlan struct {
	inserts []*scheduledInsert
	dirty   []*trackedEntry
	deletes []*scheduledRemove
}

func (p *flushPlan) empty() bool { return len(p.inserts)+len(p.dirty)+len(p.deletes) == 0 }

// planFlush orders the schedule (UOW-004 on a cycle) and finds the dirty tracked instances.
func (db *Db) planFlush() (*flushPlan, error) {
	inserts, removes := db.pendingWork()
	dirty, err := db.dirtyEntries()
	if err != nil {
		return nil, err
	}

	var seeds []reflect.Type
	for _, scheduled := range inserts {
		seeds = append(seeds, scheduled.m.Type)
	}
	for _, scheduled := range removes {
		seeds = append(seeds, scheduled.m.Type)
	}
	for _, entry := range dirty {
		seeds = append(seeds, entry.m.Type)
	}
	ranks, err := db.rankTypes(seeds)
	if err != nil {
		return nil, err
	}

	orderedInserts, err := db.orderInserts(inserts, ranks)
	if err != nil {
		return nil, err
	}
	orderedDeletes, err := db.orderDeletes(removes, ranks)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(dirty, func(i, j int) bool {
		ri, rj := ranks[dirty[i].m.Type], ranks[dirty[j].m.Type]
		if ri != rj {
			return ri < rj
		}
		return dirty[i].seq < dirty[j].seq
	})
	return &flushPlan{inserts: orderedInserts, dirty: dirty, deletes: orderedDeletes}, nil
}

// pendingWork is the schedule still to write. Work done immediately is dropped (Clarifications): a scheduled
// insert already written by Insert (it is tracked now) or a scheduled removal already deleted by Delete (it
// is not) is done, not work.
func (db *Db) pendingWork() ([]*scheduledInsert, []*scheduledRemove) {
	uow := db.schedule()
	var inserts []*scheduledInsert
	for _, scheduled := range uow.inserts {
		if db.entryOf(scheduled.ptr) == nil {
			inserts = append(inserts, scheduled)
		}
	}
	var removes []*scheduledRemove
	for _, scheduled := range uow.removes {
		if db.entryOf(scheduled.ptr) != nil {
			removes = append(removes, scheduled)
		}
	}
	return inserts, removes
}

// dirtyEntries lists the tracked instances an update would write something for, in registration order, not
// scheduled for removal. "Dirty" for the update phase means a changed column an update would write: an
// instance whose only differences are key/version/generated values is clean. A dirty instance of a
// read-only source is not skipped: its update refuses CRUD-003 and so does the flush (Clarifications).
func (db *Db) dirtyEntries() ([]*trackedEntry, error) {
	uow := db.schedule()
	var dirty []*trackedEntry
	for _, entry := range db.tracker.all() {
		if uow.removeOf[entry.ptr.Interface()] != nil {
			continue
		}
		changed, err := db.tracker.changed(entry)
		if err != nil {
			return nil, err
		}
		if hasWritableColumn(changed) {
			dirty = append(dirty, entry)
		}
	}
	return dirty, nil
}

// hasWritableColumn reports whether a change list holds a column an update would
// write: key, version, and generated values are never in a SET (session.md Clarifications).
func hasWritableColumn(changed []*core.PropertyMap) bool {
	for _, p := range changed {
		if !p.IsKey && !p.IsVersion && !p.IsGenerated {
			return true
		}
	}
	return false
}

// --- type order -------------------------------------------------------------

// typeRanks is the foreign-key rank of each entity type: a type ranks after
// every type its foreign keys reference — its many-to-one targets, and the
// owners whose one-to-many/one-to-one navigations point at it. Types in a
// reference cycle share a rank (the schedule decides among them; spec/unit-of-work.md Clarifications,
// ADR-0036: type rank).
type typeRanks map[reflect.Type]int

// rankTypes ranks the seed types and everything reachable from them through
// relationships (a rank is only meaningful against the types it can depend on).
// Strongly connected components get one rank each (Tarjan), so the result does
// not depend on the order the seeds arrive in.
func (db *Db) rankTypes(seeds []reflect.Type) (typeRanks, error) {
	dependsOn := map[reflect.Type][]reflect.Type{}
	seen := map[reflect.Type]bool{}
	var discovered []reflect.Type

	var discover func(t reflect.Type) error
	discover = func(t reflect.Type) error {
		if seen[t] {
			return nil
		}
		seen[t] = true
		discovered = append(discovered, t)
		m, err := db.maps.Load(t)
		if err != nil {
			return err
		}
		for _, r := range m.Relationships {
			switch r.Kind {
			case core.RelationshipManyToOne:
				dependsOn[t] = append(dependsOn[t], r.TargetType)
			case core.RelationshipOneToMany, core.RelationshipOneToOne:
				dependsOn[r.TargetType] = append(dependsOn[r.TargetType], t)
			default:
				continue // a many-to-many link is its own entity, ranked through its own many-to-one navigations
			}
			if err := discover(r.TargetType); err != nil {
				return err
			}
		}
		return nil
	}
	for _, seed := range seeds {
		if err := discover(seed); err != nil {
			return nil, err
		}
	}

	// Tarjan emits a component only after every component it depends on.
	index := map[reflect.Type]int{}
	lowLink := map[reflect.Type]int{}
	onStack := map[reflect.Type]bool{}
	component := map[reflect.Type]int{}
	var stack []reflect.Type
	var components [][]reflect.Type
	next := 0

	var connect func(t reflect.Type)
	connect = func(t reflect.Type) {
		index[t], lowLink[t] = next, next
		next++
		stack = append(stack, t)
		onStack[t] = true
		for _, dependency := range dependsOn[t] {
			if _, visited := index[dependency]; !visited {
				connect(dependency)
				lowLink[t] = min(lowLink[t], lowLink[dependency])
			} else if onStack[dependency] {
				lowLink[t] = min(lowLink[t], index[dependency])
			}
		}
		if lowLink[t] == index[t] {
			var members []reflect.Type
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[top] = false
				component[top] = len(components)
				members = append(members, top)
				if top == t {
					break
				}
			}
			components = append(components, members)
		}
	}
	for _, t := range discovered {
		if _, visited := index[t]; !visited {
			connect(t)
		}
	}

	ranks := typeRanks{}
	componentRank := make([]int, len(components))
	for i, members := range components {
		rank := 0
		for _, member := range members {
			for _, dependency := range dependsOn[member] {
				if component[dependency] != i {
					rank = max(rank, componentRank[component[dependency]]+1)
				}
			}
		}
		componentRank[i] = rank
		for _, member := range members {
			ranks[member] = rank
		}
	}
	return ranks, nil
}

// --- instance order ---------------------------------------------------------

// orderInserts is the insert phase's order: a child after every scheduled
// parent it references (a non-nil many-to-one navigation, or the owner that
// cascaded it); among the rest, type rank then schedule order. UOW-004 on a cycle.
//
// Instance dependencies are navigations and cascade links only — a foreign-key value that happens to equal
// a scheduled parent's key is not one (a generated parent key does not exist yet) — and among the instances
// whose parents are all written, the lowest (type rank, schedule order) goes next (Clarifications).
func (db *Db) orderInserts(inserts []*scheduledInsert, ranks typeRanks) ([]*scheduledInsert, error) {
	position := map[any]int{}
	for i, scheduled := range inserts {
		position[scheduled.ptr] = i
	}
	dependencies := make([][]int, len(inserts))
	for i, scheduled := range inserts {
		for _, relationship := range scheduled.m.Relationships {
			if relationship.Kind != core.RelationshipManyToOne {
				continue
			}
			targets, _ := navigationTargets(scheduled.ptr, relationship)
			for _, target := range targets {
				if parent, ok := position[target]; ok {
					dependencies[i] = append(dependencies[i], parent)
				}
			}
		}
		for _, link := range scheduled.links {
			if link.relationship.Kind == core.RelationshipManyToOne {
				continue // the owner references this child: the navigation above already orders the child first
			}
			if parent, ok := position[link.owner]; ok {
				dependencies[i] = append(dependencies[i], parent)
			}
		}
	}

	order, ok := dependencyOrder(len(inserts), dependencies, func(a, b int) bool {
		ra, rb := ranks[inserts[a].m.Type], ranks[inserts[b].m.Type]
		if ra != rb {
			return ra < rb
		}
		return inserts[a].order < inserts[b].order
	})
	if !ok {
		return nil, cycleError("inserts", insertTypes(inserts, order))
	}
	ordered := make([]*scheduledInsert, len(order))
	for i, index := range order {
		ordered[i] = inserts[index]
	}
	return ordered, nil
}

// orderDeletes is the delete phase's order, children first: a parent after every
// scheduled child that references it (a non-nil many-to-one navigation to it, or
// a non-nil one-to-many/one-to-one navigation of it holding the child); among the
// rest, reversed type rank then schedule order. UOW-004 on a cycle. As for inserts, "references it" means
// a non-nil navigation (many-to-one child to parent, or a parent's one-to-many/one-to-one holding the
// child), not a foreign-key value.
func (db *Db) orderDeletes(removes []*scheduledRemove, ranks typeRanks) ([]*scheduledRemove, error) {
	position := map[any]int{}
	for i, scheduled := range removes {
		position[scheduled.ptr] = i
	}
	dependencies := make([][]int, len(removes)) // dependencies[i]: the instances that must be deleted before i
	for i, scheduled := range removes {
		for _, relationship := range scheduled.m.Relationships {
			targets, _ := navigationTargets(scheduled.ptr, relationship)
			for _, target := range targets {
				other, ok := position[target]
				if !ok {
					continue
				}
				switch relationship.Kind {
				case core.RelationshipManyToOne:
					dependencies[other] = append(dependencies[other], i) // this child goes before the parent it references
				case core.RelationshipOneToMany, core.RelationshipOneToOne:
					dependencies[i] = append(dependencies[i], other) // this parent goes after the child it holds
				}
			}
		}
	}

	order, ok := dependencyOrder(len(removes), dependencies, func(a, b int) bool {
		ra, rb := ranks[removes[a].m.Type], ranks[removes[b].m.Type]
		if ra != rb {
			return ra > rb
		}
		return removes[a].order < removes[b].order
	})
	if !ok {
		var types []string
		for _, scheduled := range removes {
			types = append(types, scheduled.m.EntityName())
		}
		return nil, cycleError("deletes", uniqueSorted(types))
	}
	ordered := make([]*scheduledRemove, len(order))
	for i, index := range order {
		ordered[i] = removes[index]
	}
	return ordered, nil
}

// insertTypes names the entity types of the inserts a cycle left unordered.
func insertTypes(inserts []*scheduledInsert, ordered []int) []string {
	done := map[int]bool{}
	for _, index := range ordered {
		done[index] = true
	}
	var types []string
	for i, scheduled := range inserts {
		if !done[i] {
			types = append(types, scheduled.m.EntityName())
		}
	}
	return uniqueSorted(types)
}

func uniqueSorted(names []string) []string {
	sort.Strings(names)
	var unique []string
	for _, name := range names {
		if len(unique) == 0 || unique[len(unique)-1] != name {
			unique = append(unique, name)
		}
	}
	return unique
}

func cycleError(phase string, types []string) error {
	return core.Errorf("UOW-004", strings.Join(types, ", "),
		"the scheduled %s reference each other in a cycle through their navigations, so no foreign-key order exists; break the cycle (persist or remove the instances in separate flushes) before flushing", phase)
}

// dependencyOrder is the one ordering both phases use: a topological order of
// n nodes in which dependencies[i] must precede i, taking the smallest ready
// node under less at each step. ok is false when a cycle leaves nodes unordered
// (the returned slice then holds only the ones that were ordered).
func dependencyOrder(n int, dependencies [][]int, less func(a, b int) bool) (order []int, ok bool) {
	waiting := make([]int, n)
	dependents := make([][]int, n)
	for i, list := range dependencies {
		seen := map[int]bool{}
		for _, dependency := range list {
			if dependency == i || seen[dependency] {
				continue
			}
			seen[dependency] = true
			waiting[i]++
			dependents[dependency] = append(dependents[dependency], i)
		}
	}

	ready := &readyHeap{less: less}
	for i := 0; i < n; i++ {
		if waiting[i] == 0 {
			heap.Push(ready, i)
		}
	}
	for ready.Len() > 0 {
		node := heap.Pop(ready).(int)
		order = append(order, node)
		for _, dependent := range dependents[node] {
			waiting[dependent]--
			if waiting[dependent] == 0 {
				heap.Push(ready, dependent)
			}
		}
	}
	return order, len(order) == n
}

type readyHeap struct {
	nodes []int
	less  func(a, b int) bool
}

func (h *readyHeap) Len() int           { return len(h.nodes) }
func (h *readyHeap) Less(i, j int) bool { return h.less(h.nodes[i], h.nodes[j]) }
func (h *readyHeap) Swap(i, j int)      { h.nodes[i], h.nodes[j] = h.nodes[j], h.nodes[i] }
func (h *readyHeap) Push(x any)         { h.nodes = append(h.nodes, x.(int)) }
func (h *readyHeap) Pop() any {
	last := h.nodes[len(h.nodes)-1]
	h.nodes = h.nodes[:len(h.nodes)-1]
	return last
}

// --- execution --------------------------------------------------------------

// executeFlush runs the plan's statements in order, journaling how to undo
// each one's effect on the session's in-memory state.
func (db *Db) executeFlush(ctx context.Context, plan *flushPlan, journal *flushJournal) (FlushSummary, error) {
	var summary FlushSummary

	for _, scheduled := range plan.inserts {
		if err := flowKeys(db, scheduled); err != nil {
			return summary, err
		}
		journal.recordInsert(db, scheduled)
		if err := insertEntity(ctx, db, scheduled.m.Type, scheduled.ptr); err != nil {
			return summary, err
		}
		summary.Inserted++
	}

	for _, entry := range plan.dirty {
		journal.recordUpdate(entry)
		wrote, err := updateEntity(ctx, db, entry.m.Type, entry.ptr.Interface())
		if err != nil {
			return summary, err
		}
		if wrote {
			summary.Updated++
		}
	}

	for _, scheduled := range plan.deletes {
		journal.recordDelete(db, scheduled)
		if err := deleteEntity(ctx, db, scheduled.m.Type, scheduled.ptr); err != nil {
			return summary, err
		}
		summary.Deleted++
	}
	return summary, nil
}

// flowKeys copies keys into the foreign keys of an instance about to be
// inserted (spec/unit-of-work.md "Keys flow before each insert"): every
// non-nil many-to-one navigation copies its target's key — the navigation is
// authoritative inside the unit of work — and a child a persist cascade
// reached takes its owner's key into the foreign key the relationship
// declares, so a database-generated parent key reaches its children.
func flowKeys(db *Db, scheduled *scheduledInsert) error {
	// A non-nil many-to-one navigation copies its target's key as it is, even when the target is neither
	// scheduled nor tracked (a zero part included): the database's foreign key is the authority that refuses it
	// (Clarifications: keys flow only before inserts).
	for _, relationship := range scheduled.m.Relationships {
		if relationship.Kind != core.RelationshipManyToOne {
			continue
		}
		targets, _ := navigationTargets(scheduled.ptr, relationship)
		if len(targets) == 0 {
			continue
		}
		targetMap, err := db.maps.Load(relationship.TargetType)
		if err != nil {
			return err
		}
		if err := copyKey(targetMap, targets[0], scheduled.m, scheduled.ptr, relationship.ForeignKeyProperties); err != nil {
			return err
		}
	}
	for _, link := range scheduled.links {
		if link.relationship.Kind != core.RelationshipOneToMany && link.relationship.Kind != core.RelationshipOneToOne {
			continue
		}
		if err := copyKey(link.ownerMap, link.owner, scheduled.m, scheduled.ptr, link.relationship.ForeignKeyProperties); err != nil {
			return err
		}
	}
	return nil
}

// copyKey writes the key of source into the named foreign-key properties of
// target, part by part in key order. An arity disagreement is a loader error
// the map assembly already refused, so a short list is skipped, not guessed.
func copyKey(sourceMap *core.EntityMap, source any, targetMap *core.EntityMap, target any, foreignKeys []string) error {
	if len(sourceMap.KeyProperties) != len(foreignKeys) {
		return nil
	}
	for i, keyProperty := range sourceMap.KeyProperties {
		foreignKey := targetMap.Property(foreignKeys[i])
		if foreignKey == nil {
			return core.Errorf("REL-003", targetMap.EntityName()+"."+foreignKeys[i], "is not a mapped property of %s", targetMap.EntityName())
		}
		if err := foreignKey.Set(target, keyProperty.Get(source)); err != nil {
			return err
		}
	}
	return nil
}

// --- undo journal -----------------------------------------------------------

// flushJournal remembers how to put the session's in-memory state back when a
// flush fails: the database side is the transaction's rollback; this is the
// identity map, the snapshots, the versions, and the generated keys.
type flushJournal struct {
	undo []func()
}

func (j *flushJournal) rollback() {
	for i := len(j.undo) - 1; i >= 0; i-- {
		j.undo[i]()
	}
}

// recordInsert is registered before the insert runs, so a partial failure is undone too.
func (j *flushJournal) recordInsert(db *Db, scheduled *scheduledInsert) {
	m := scheduled.m
	priorKeys := make([]any, len(m.KeyProperties))
	for i, key := range m.KeyProperties {
		priorKeys[i] = key.Get(scheduled.ptr)
	}
	var displaced *trackedEntry
	if key, ok := keyOf(m, scheduled.ptr); ok {
		displaced = db.tracker.find(m.Type, key)
	}
	j.undo = append(j.undo, func() {
		if entry := db.entryOf(scheduled.ptr); entry != nil {
			db.tracker.forget(entry)
		}
		if displaced != nil {
			db.tracker.restore(displaced)
		}
		for i, key := range m.KeyProperties {
			_ = key.Set(scheduled.ptr, priorKeys[i]) // the value came from Get, so it always assigns back
		}
	})
}

// recordUpdate restores the snapshot and the version an update moved.
func (j *flushJournal) recordUpdate(entry *trackedEntry) {
	snapshot := append([]any(nil), entry.snapshot...)
	version := entry.m.VersionProperty
	var versionValue any
	if version != nil {
		versionValue = version.Get(entry.ptr.Interface())
	}
	j.undo = append(j.undo, func() {
		copy(entry.snapshot, snapshot)
		if version != nil {
			_ = version.Set(entry.ptr.Interface(), versionValue) // the value came from Get, so it always assigns back
		}
	})
}

// recordDelete re-registers the instance a delete forgot, exactly as it was.
func (j *flushJournal) recordDelete(db *Db, scheduled *scheduledRemove) {
	entry := db.entryOf(scheduled.ptr)
	j.undo = append(j.undo, func() {
		if entry != nil {
			db.tracker.restore(entry)
		}
	})
}
