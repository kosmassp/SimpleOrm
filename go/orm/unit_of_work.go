package orm

import (
	"fmt"
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The unit of work — schedule, then flush (Level 3 M2, ADR-0034 §7c,
// spec/unit-of-work.md). A tracked session schedules inserts (Persist) and
// removals (Remove) and writes them, with every dirty tracked instance, in one
// explicit Flush. Nothing is written or loaded before Flush; cascades are
// declared on navigations (`cascade=persist+remove`), never inferred, and walk
// only what is already in memory.
//
// Go shape (CODING-STANDARD §10): pointers are the tracked shape, so Persist and
// Remove take a *T; a collection navigation that was never loaded is a nil slice
// and one that is loaded but empty a non-nil empty slice — that distinction is
// what a remove cascade's UOW-005 reads (nothing loads to decide); a nil
// singular navigation cascades nothing.
//
// Persist and Remove plan first and commit after: a refusal anywhere in a
// cascade schedules nothing.

// FlushSummary is what a flush wrote: statements per kind (spec/unit-of-work.md "The calls").
type FlushSummary struct {
	Inserted int
	Updated  int
	Deleted  int
}

// unitOfWork is a tracked session's schedule: the instances to insert and the
// tracked instances to delete, each in schedule order.
type unitOfWork struct {
	inserts  []*scheduledInsert
	insertOf map[any]*scheduledInsert
	removes  []*scheduledRemove
	removeOf map[any]*scheduledRemove
	counter  uint64
}

// scheduledInsert is an instance Persist scheduled. direct is true when the
// caller persisted it (not only a cascade reached it); links are the persist
// cascades that reached it — the owner whose key it takes at flush.
type scheduledInsert struct {
	ptr    any // *E
	m      *core.EntityMap
	direct bool
	links  []cascadeLink
	order  uint64
}

// cascadeLink records a persist cascade: owner's relationship reached the
// scheduled child. A one-to-many/one-to-one link makes the child take the
// owner's key into the declared foreign key; a many-to-one link is the
// owner referencing the child, which the navigation itself already orders.
type cascadeLink struct {
	owner        any // *E
	ownerMap     *core.EntityMap
	relationship *core.RelationshipMap
}

// scheduledRemove is a tracked instance Remove scheduled for deletion.
type scheduledRemove struct {
	ptr   any // *E
	m     *core.EntityMap
	order uint64
}

func newUnitOfWork() *unitOfWork {
	return &unitOfWork{insertOf: map[any]*scheduledInsert{}, removeOf: map[any]*scheduledRemove{}}
}

// schedule is the session's unit of work, created on first use; reads that must
// not allocate go through HasPendingWork.
func (db *Db) schedule() *unitOfWork {
	if db.uow == nil {
		db.uow = newUnitOfWork()
	}
	return db.uow
}

// requireUnitOfWork is the UOW-003 gate: the unit of work needs the identity map and the snapshots.
func (db *Db) requireUnitOfWork(operation string) error {
	if db.tracker == nil {
		return core.Errorf("UOW-003", "session",
			"%s needs a tracked session: the unit of work works from the identity map and the snapshots; open the session with Options{Tracking: TrackingTracked}", operation)
	}
	return nil
}

// HasPendingWork reports whether anything is scheduled (an insert or a
// removal). Dirty tracked instances flush regardless of it (spec/unit-of-work.md).
func (db *Db) HasPendingWork() bool {
	return db.uow != nil && (len(db.uow.inserts) > 0 || len(db.uow.removes) > 0)
}

// Persist schedules an insert for an instance the session does not track, and
// cascades Persist over the initialized navigations that declare it. A tracked
// instance schedules nothing itself — its changes flush as updates — but its
// declared cascades still reach new children. UOW-003 on an untracked session;
// UOW-006 for an instance scheduled for removal; CRUD-003/CRUD-002 for a
// read-only or keyless entity; nothing is scheduled when any part refuses.
func Persist[T any](db *Db, entity *T) error {
	if err := db.requireUnitOfWork("Persist"); err != nil {
		return err
	}
	if entity == nil {
		return fmt.Errorf("orm.Persist: entity is nil")
	}
	// SPEC-GAP: Persist and Remove take no context and return no summary — the spec says nothing about
	// their signatures; they do no I/O, so Go's "every I/O function takes a context" rule does not apply.
	plan := &persistPlan{db: db, visited: map[any]bool{}, added: map[any]*scheduledInsert{}}
	if err := plan.visit(reflect.TypeFor[T](), entity, nil); err != nil {
		return err
	}
	plan.commit()
	return nil
}

// persistPlan is Persist's walk before anything is scheduled: the inserts it
// would add, the links it would record, and the instances that become direct.
type persistPlan struct {
	db      *Db
	visited map[any]bool
	adds    []*scheduledInsert
	added   map[any]*scheduledInsert
	links   []plannedLink
	directs []*scheduledInsert
}

type plannedLink struct {
	child any
	link  cascadeLink
}

// visit plans one instance; via is the cascade that reached it (nil for the caller's own call).
func (p *persistPlan) visit(entityType reflect.Type, ptr any, via *cascadeLink) error {
	m, err := p.db.maps.Load(entityType)
	if err != nil {
		return err
	}
	uow := p.db.schedule()

	scheduled := uow.insertOf[ptr]
	if scheduled == nil {
		scheduled = p.added[ptr]
	}

	if p.visited[ptr] {
		// Reached again (a second owner, or a cycle of cascades): the walk below it is done; only the link and the direct flag are new.
		p.note(ptr, scheduled, via)
		return nil
	}
	p.visited[ptr] = true

	// SPEC-GAP: the order of Persist's refusals — UOW-006 (scheduled for removal) before the tracked check
	// and before CRUD-003/CRUD-002 — is not in the spec; a cascade reaching a removed instance is UOW-006 too.
	if uow.removeOf[ptr] != nil {
		return core.Errorf("UOW-006", m.EntityName(),
			"the instance is scheduled for removal; it cannot also be persisted in the same unit of work")
	}

	// SPEC-GAP: Persist of a tracked instance "schedules nothing" — whether its declared cascades still
	// walk is unspecified. Here they do (the spec's "a never-loaded collection of a tracked instance
	// cascades nothing" only has a point if an initialized one cascades), so a new child added to a
	// loaded collection is scheduled by persisting the loaded owner. Persisting an already-scheduled
	// instance again is a no-op (it only becomes "direct").
	if scheduled == nil && p.db.entryOf(ptr) == nil {
		// An untracked, unscheduled instance: a new insert, which must be a keyed table row.
		if m.Kind != core.RelationTable {
			return core.Errorf("CRUD-003", m.EntityName(), "is %s-backed and read-only; writes need a table", m.Kind)
		}
		if len(m.KeyProperties) == 0 {
			return core.NewError("CRUD-002", m.EntityName(), "the entity defines no key")
		}
		uow.counter++
		scheduled = &scheduledInsert{ptr: ptr, m: m, order: uow.counter}
		p.adds = append(p.adds, scheduled)
		p.added[ptr] = scheduled
	}
	p.note(ptr, scheduled, via)

	// SPEC-GAP: schedule order of a cascade is pre-order — the owner first, then its targets in
	// collection order, depth first; a child already scheduled keeps its earlier position. A many-to-one
	// persist cascade schedules the referenced parent the same way (the spec shows only one-to-many).
	for _, relationship := range m.Relationships {
		if !relationship.Cascade.Has(core.CascadePersist) {
			continue
		}
		targets, _ := navigationTargets(ptr, relationship)
		for _, target := range targets {
			link := &cascadeLink{owner: ptr, ownerMap: m, relationship: relationship}
			if err := p.visit(relationship.TargetType, target, link); err != nil {
				return err
			}
		}
	}
	return nil
}

// note records what reaching ptr means: a link when a cascade reached a
// scheduled child, the direct flag when the caller persisted it.
func (p *persistPlan) note(ptr any, scheduled *scheduledInsert, via *cascadeLink) {
	if scheduled == nil {
		return // a tracked instance: nothing to insert, nothing to link
	}
	if via == nil {
		p.directs = append(p.directs, scheduled)
		return
	}
	p.links = append(p.links, plannedLink{child: ptr, link: *via})
}

// commit applies the plan: the new inserts, the links, the direct flags.
func (p *persistPlan) commit() {
	uow := p.db.schedule()
	for _, add := range p.adds {
		uow.inserts = append(uow.inserts, add)
		uow.insertOf[add.ptr] = add
	}
	for _, scheduled := range p.directs {
		scheduled.direct = true
	}
	for _, planned := range p.links {
		child := uow.insertOf[planned.child]
		if child == nil {
			continue
		}
		duplicate := false
		for _, existing := range child.links {
			if existing.owner == planned.link.owner && existing.relationship == planned.link.relationship {
				duplicate = true
				break
			}
		}
		if !duplicate {
			child.links = append(child.links, planned.link)
		}
	}
}

// Remove schedules a delete for a tracked instance, and cascades Remove over
// the navigations that declare it. An instance scheduled for insert has its
// insert cancelled instead (nothing was written, so nothing is deleted) along
// with the inserts its persist cascade scheduled. An untracked instance is
// UOW-001; a remove cascade over a never-loaded collection is UOW-005; a nil
// singular navigation cascades nothing; nothing is scheduled when any part refuses.
func Remove[T any](db *Db, entity *T) error {
	if err := db.requireUnitOfWork("Remove"); err != nil {
		return err
	}
	if entity == nil {
		return fmt.Errorf("orm.Remove: entity is nil")
	}
	plan := &removePlan{db: db, visited: map[any]bool{}}
	if err := plan.visit(reflect.TypeFor[T](), entity); err != nil {
		return err
	}
	plan.commit()
	return nil
}

// removePlan is Remove's walk before anything is scheduled.
type removePlan struct {
	db      *Db
	visited map[any]bool
	adds    []*scheduledRemove
	cancels []*scheduledInsert
}

func (p *removePlan) visit(entityType reflect.Type, ptr any) error {
	if p.visited[ptr] {
		return nil
	}
	p.visited[ptr] = true

	m, err := p.db.maps.Load(entityType)
	if err != nil {
		return err
	}
	uow := p.db.schedule()

	if scheduled := uow.insertOf[ptr]; scheduled != nil {
		p.cancels = append(p.cancels, scheduled)
		return nil
	}
	if uow.removeOf[ptr] != nil {
		return nil // already scheduled for removal
	}
	// SPEC-GAP: a remove cascade reaching an instance the session neither tracks nor has scheduled for
	// insert is UOW-001 (the spec states UOW-001 only for the caller's own Remove); removing an instance
	// already scheduled for removal again is a no-op.
	if p.db.entryOf(ptr) == nil {
		return core.NewError("UOW-001", m.EntityName(),
			"the session does not track this instance, so it knows no row to delete; read it through the session first, or delete by key")
	}
	if m.Kind != core.RelationTable {
		return core.Errorf("CRUD-003", m.EntityName(), "is %s-backed and read-only; writes need a table", m.Kind)
	}

	uow.counter++
	p.adds = append(p.adds, &scheduledRemove{ptr: ptr, m: m, order: uow.counter})

	for _, relationship := range m.Relationships {
		if !relationship.Cascade.Has(core.CascadeRemove) {
			continue
		}
		targets, initialized := navigationTargets(ptr, relationship)
		// SPEC-GAP: UOW-005 is the never-loaded *collection* (a nil slice); a nil singular navigation of
		// any kind cascades nothing, as the spec says; an empty non-nil slice is loaded-and-empty.
		if !initialized && relationship.Kind == core.RelationshipOneToMany {
			return core.Errorf("UOW-005", m.EntityName()+"."+relationship.PropertyName,
				"the collection was never loaded, so a remove cascade cannot know its members; load it (orm.Load) or remove the children explicitly — nothing loads implicitly")
		}
		for _, target := range targets {
			if err := p.visit(relationship.TargetType, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *removePlan) commit() {
	uow := p.db.schedule()
	for _, add := range p.adds {
		uow.removes = append(uow.removes, add)
		uow.removeOf[add.ptr] = add
	}
	uow.cancel(p.cancels)
}

// cancel drops scheduled inserts and, transitively, the inserts only their
// persist cascade had scheduled (a child persisted directly stays, minus the link).
func (u *unitOfWork) cancel(initial []*scheduledInsert) {
	cancelled := map[*scheduledInsert]bool{}
	for _, scheduled := range initial {
		cancelled[scheduled] = true
	}
	// SPEC-GAP: "cancels the insert (and the inserts it cascaded)" — a child that was also persisted
	// directly, or reached by another still-scheduled owner, stays scheduled; the rule is transitive.
	for changed := len(cancelled) > 0; changed; {
		changed = false
		for _, scheduled := range u.inserts {
			if cancelled[scheduled] {
				continue
			}
			kept := scheduled.links[:0:0]
			for _, link := range scheduled.links {
				if owner := u.insertOf[link.owner]; owner != nil && cancelled[owner] {
					continue
				}
				kept = append(kept, link)
			}
			dropped := len(kept) != len(scheduled.links)
			scheduled.links = kept
			if dropped && !scheduled.direct && len(kept) == 0 {
				cancelled[scheduled] = true
				changed = true
			}
		}
	}

	remaining := u.inserts[:0:0]
	for _, scheduled := range u.inserts {
		if cancelled[scheduled] {
			delete(u.insertOf, scheduled.ptr)
			continue
		}
		remaining = append(remaining, scheduled)
	}
	u.inserts = remaining
}

// clear empties the schedule after a successful flush.
func (u *unitOfWork) clear() {
	u.inserts = nil
	u.removes = nil
	u.insertOf = map[any]*scheduledInsert{}
	u.removeOf = map[any]*scheduledRemove{}
}

// navigationTargets lists a navigation's current targets as *E pointers and
// whether the navigation is initialized: a non-nil pointer, or a non-nil slice
// (an empty one is loaded-and-empty). Nothing loads to answer (§2, no hidden
// queries); a nil element of a collection is skipped.
func navigationTargets(entity any, relationship *core.RelationshipMap) (targets []any, initialized bool) {
	field := reflect.ValueOf(entity).Elem().FieldByName(relationship.PropertyName)
	if !field.IsValid() {
		return nil, false
	}
	switch field.Kind() {
	case reflect.Pointer:
		if field.IsNil() {
			return nil, false
		}
		return []any{field.Interface()}, true
	case reflect.Slice:
		if field.IsNil() {
			return nil, false
		}
		for i := 0; i < field.Len(); i++ {
			element := field.Index(i)
			if element.Kind() == reflect.Pointer {
				if element.IsNil() {
					continue
				}
				targets = append(targets, element.Interface())
				continue
			}
			targets = append(targets, element.Addr().Interface())
		}
		return targets, true
	}
	return nil, false
}
