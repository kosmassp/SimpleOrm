package orm

import (
	"context"
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
)

// Tracking — the identity map and change snapshots (Level 3 M1, ADR-0034 add.1,
// spec/session.md "Tracking"). A session opened with Options.Tracking =
// TrackingTracked keeps one instance per (entity type, key) and a snapshot of
// each instance's mapped values; the default session is the Level 0–2 session
// untouched. Nothing here adds a round trip: tracking only decides which
// instance a read returns and which columns an update writes.
//
// Go shape (CODING-STANDARD §10): the tracked instance is a pointer, so the
// pointer-shaped results are the tracked ones — `orm.From[*User](db)`,
// `orm.Query[A, *User]`, `orm.Get[*User]`, `[]*Transaction` navigations,
// `orm.GetOrDefault[User]` (which already returns *User). A value-shaped
// result (`[]User`) is a copy: it is never registered, and for a key the
// session already tracks it is a copy of the tracked instance's current state
// (the tracked instance wins).
//
// A value-shaped result is never registered and, for a known key, carries the tracked
// instance's current state; writing through such a copy is UOW-002 when another instance holds
// its key (spec/session.md Clarifications, ADR-0035: "value-shaped results").

// IsTracking reports whether the session is in tracked mode.
func (db *Db) IsTracking() bool { return db.tracker != nil }

// IsTracked reports whether the session tracks this very instance (a pointer
// it registered by reading, inserting, or updating it). It never fails: an
// untracked session, a value, a nil, or a stranger all answer false.
func (db *Db) IsTracked(entity any) bool {
	return db.entryOf(entity) != nil
}

// ChangesOf lists the mapped property names whose value differs from the
// snapshot, in map order, dotted for owned members ("Address.City"). UOW-001
// when the session does not track the instance — every instance of an
// untracked session included.
func ChangesOf[T any](db *Db, entity *T) ([]string, error) {
	entry, err := db.requireTracked(entity, reflect.TypeFor[T]())
	if err != nil {
		return nil, err
	}
	changed, err := db.tracker.changed(entry)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(changed))
	for i, p := range changed {
		names[i] = p.PropertyName
	}
	return names, nil
}

// IsDirty reports whether any mapped value differs from the snapshot; UOW-001 as ChangesOf.
func IsDirty[T any](db *Db, entity *T) (bool, error) {
	entry, err := db.requireTracked(entity, reflect.TypeFor[T]())
	if err != nil {
		return false, err
	}
	changed, err := db.tracker.changed(entry)
	return len(changed) > 0, err
}

// Refresh is the explicit overwrite: it re-reads the row by key, applies every
// mapped value onto the instance (loaded navigations stay as they are), and
// resets the snapshot; a missing row is CRUD-001. It works in both modes and
// never changes which instances are tracked. An instance that is not the
// session's instance for its key — a second copy, or a tracked instance whose
// key fields were edited — is UOW-002; an unknown instance with a free key has
// the row applied and stays unregistered.
func Refresh[T any](ctx context.Context, db *Db, entity *T) error {
	entityType := reflect.TypeFor[T]()
	m, err := db.maps.Load(entityType)
	if err != nil {
		return err
	}
	if err := requireNamedRelation(m, entityType, "refresh needs a named relation"); err != nil {
		return err
	}

	// The identity gate (spec/session.md Clarifications, ADR-0035): a tracked instance whose key was
	// changed, or one that is not the session's instance for its key, is UOW-002; an instance the
	// session does not know, with a free key, has the row applied and is not registered.
	entry, err := db.writeIdentity(m, entity)
	if err != nil {
		return err
	}
	key, err := m.KeyValues(entity)
	if err != nil {
		return err
	}
	keyValues, err := validateKey(m, key)
	if err != nil {
		return err
	}

	fresh, err := readRowByKey(ctx, db, m, entityType, keyValues)
	if err != nil {
		return err
	}
	if !fresh.IsValid() {
		return core.Errorf("CRUD-001", entityType.Name(), "refresh found no row with key (%s)", formatKey(key))
	}

	if err := applyRow(m, entity, fresh.Interface()); err != nil {
		return err
	}
	if entry != nil {
		snapshot, err := db.tracker.snapshotOf(m, entity)
		if err != nil {
			return err
		}
		entry.snapshot = snapshot
	}
	return nil
}

// applyRow copies every mapped value of fresh (a freshly materialized *E) onto
// entity. An owned navigation is copied whole, so an all-NULL row clears a
// nullable one (ADR-0030); navigations (relationships) are not mapped values
// and stay untouched.
func applyRow(m *core.EntityMap, entity any, fresh any) error {
	target := reflect.ValueOf(entity).Elem()
	source := reflect.ValueOf(fresh).Elem()
	for _, owned := range m.OwnedTypes {
		target.FieldByIndex(owned.Index).Set(source.FieldByIndex(owned.Index))
	}
	for _, p := range m.Properties {
		if p.Owner != nil {
			continue
		}
		if err := p.Set(entity, p.Get(fresh)); err != nil {
			return err
		}
	}
	return nil
}

// --- the session's side --------------------------------------------------

// entryOf is the tracked entry for exactly this instance, or nil.
func (db *Db) entryOf(entity any) *trackedEntry {
	if db.tracker == nil || entity == nil || reflect.TypeOf(entity).Kind() != reflect.Pointer {
		return nil
	}
	return db.tracker.byPtr[entity]
}

// requireTracked is the UOW-001 gate of ChangesOf/IsDirty.
func (db *Db) requireTracked(entity any, entityType reflect.Type) (*trackedEntry, error) {
	if db.tracker == nil {
		return nil, core.NewError("UOW-001", entityType.Name(),
			"the session is untracked (Options.Tracking); open it with TrackingTracked to ask about changes")
	}
	if entry := db.entryOf(entity); entry != nil {
		return entry, nil
	}
	return nil, core.NewError("UOW-001", entityType.Name(),
		"this instance is not tracked by the session; read, insert, or update it through the session first")
}

// tracking returns m when reads of it are tracked in this session — tracked
// mode and a keyed entity — else nil: keyless entities (statements, views
// without a key) have no identity and are never tracked (spec/session.md).
// A keyed read-only entity (a view with a declared key) is tracked like a table; its writes
// still refuse CRUD-003 (spec/session.md Clarifications, ADR-0035: "which types are tracked").
func (db *Db) tracking(m *core.EntityMap) *core.EntityMap {
	if db.tracker == nil || m == nil || len(m.KeyProperties) == 0 {
		return nil
	}
	return m
}

// resultMapFor is tracking for a result type the registry names: only a type
// the mapper reads as an entity (it carries mapping declarations) is tracked;
// a plain result struct is never tracked. A map that fails to load is nil here —
// the mapper reports the same error when it builds the plan.
func (db *Db) resultMapFor(resultType reflect.Type) *core.EntityMap {
	if db.tracker == nil {
		return nil
	}
	t := stripPointer(resultType)
	if t.Kind() != reflect.Struct || !metadata.HasMappingDeclarations(t) {
		return nil
	}
	m, err := db.maps.Load(t)
	if err != nil {
		return nil
	}
	return db.tracking(m)
}

// keyOf is an instance's identity (§7.4) as a tuple; false when a key part is
// null — such a row is never tracked.
func keyOf(m *core.EntityMap, entity any) (core.KeyTuple, bool) {
	tuple := extractTuple(m.KeyProperties, entity)
	return tuple, !tuple.HasNull()
}

// resolveRow turns a freshly materialized row into the session's instance
// (m is a tracking map) — the one identity resolution every read path goes
// through. fresh is a *E or an E:
//   - a *E whose key is known returns the tracked pointer and drops the row's
//     values (ruling 2: the tracked instance wins; the row was still mapped, so
//     its conversion errors fired); an unknown key registers the fresh pointer
//     with its snapshot.
//   - an E (a value-shaped result) cannot hand the caller the tracked pointer,
//     so it is never registered; a known key yields a copy of the tracked
//     instance's current state.
//
// A row with a null key part is returned as it is.
func (db *Db) resolveRow(m *core.EntityMap, fresh reflect.Value) (reflect.Value, error) {
	key, ok := keyOf(m, fresh.Interface())
	if !ok {
		return fresh, nil
	}
	isPointer := fresh.Kind() == reflect.Pointer
	if entry := db.tracker.find(m.Type, key); entry != nil {
		if isPointer {
			return entry.ptr, nil
		}
		return entry.ptr.Elem(), nil
	}
	if isPointer {
		if _, err := db.tracker.register(m, fresh, key); err != nil {
			return reflect.Value{}, err
		}
	}
	return fresh, nil
}

// resolveAny is resolveRow for a materialized result of unknown shape (the
// plan's `any`); m nil means "not tracked".
func (db *Db) resolveAny(m *core.EntityMap, value any) (any, error) {
	if m == nil {
		return value, nil
	}
	resolved, err := db.resolveRow(m, reflect.ValueOf(value))
	if err != nil {
		return nil, err
	}
	return resolved.Interface(), nil
}

// --- the write side --------------------------------------------------------

// writeIdentity is the UOW-002 gate of every write from an entity (before any
// SQL): the instance must be the session's instance for its key. It returns
// the tracked entry of this very instance, or nil for an instance the session
// does not know (and for an untracked session). A tracked instance whose key
// fields no longer match its registered identity, or an unknown instance whose
// key another tracked instance holds, is UOW-002 — a key is identity and
// never updates.
func (db *Db) writeIdentity(m *core.EntityMap, entity any) (*trackedEntry, error) {
	if db.tracker == nil {
		return nil, nil
	}
	current, hasKey := keyOf(m, entity)
	if entry := db.entryOf(entity); entry != nil {
		if !hasKey || entry.key.Compare(current) != 0 {
			return nil, core.Errorf("UOW-002", m.EntityName(),
				"the key of a tracked instance changed from (%s) to (%s); a key is identity and never updates — delete and insert instead",
				formatKey([]any(entry.key)), formatKeyOf(m, entity))
		}
		return entry, nil
	}
	if !hasKey {
		return nil, nil
	}
	if holder := db.tracker.find(m.Type, current); holder != nil {
		return nil, core.Errorf("UOW-002", m.EntityName(),
			"another instance is tracked for key (%s); this instance is not the session's instance for it",
			formatKey([]any(current)))
	}
	return nil, nil
}

// track registers entity (a *E) after a write stored it, with a fresh snapshot.
func (db *Db) track(m *core.EntityMap, entity any) error {
	if db.tracking(m) == nil {
		return nil
	}
	if key, ok := keyOf(m, entity); ok {
		_, err := db.tracker.register(m, reflect.ValueOf(entity), key)
		return err
	}
	return nil
}

// forgetKey drops whatever instance is tracked for (m's type, key).
func (db *Db) forgetKey(m *core.EntityMap, key core.KeyTuple) {
	if db.tracker == nil {
		return
	}
	if entry := db.tracker.find(m.Type, key); entry != nil {
		db.tracker.forget(entry)
	}
}
