package orm

import (
	"context"
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// Detach and merge (Level 3 M3, ADR-0034 §7c, spec/unit-of-work.md "Detach and merge"): the simple
// rules, no deep graph merge. Both need a tracked session (UOW-003). Go shape (CODING-STANDARD §10):
// pointers are the tracked shape, so both take a *T; Merge returns the session's own pointer.

// Detach makes the session forget the instance: it is no longer the session's instance for its key
// (a later read materializes a fresh one) and anything scheduled for it is cancelled — an insert with
// the inserts its persist cascade scheduled, a removal dropped. The instance keeps its values. An
// instance the session neither tracks nor has scheduled is UOW-001; an untracked session is UOW-003.
func Detach[T any](db *Db, entity *T) error {
	if err := db.requireUnitOfWork("Detach"); err != nil {
		return err
	}
	if entity == nil {
		// Go cannot refuse a nil pointer by type: UOW-001 for Detach, CRUD-002 for Merge (spec/hooks.md Clarifications).
		return core.NewError("UOW-001", reflect.TypeFor[T]().Name(), "detach needs an instance, got nil")
	}

	uow := db.schedule()
	entry := db.entryOf(entity)
	insert := uow.insertOf[entity]
	remove := uow.removeOf[entity]
	if entry == nil && insert == nil && remove == nil {
		return core.NewError("UOW-001", reflect.TypeFor[T]().Name(),
			"the session neither tracks this instance nor has it scheduled; there is nothing to detach")
	}

	// By instance, never by key. Detaching a tracked owner cancels nothing it scheduled (the inserts its persist
	// cascade reached stay and still take its key at flush); a scheduled insert is cancelled with what only its
	// cascade scheduled; a scheduled removal alone is dropped (spec/unit-of-work.md "Clarifications on detach and merge").
	if insert != nil {
		uow.cancel([]*scheduledInsert{insert})
	}
	if remove != nil {
		uow.dropRemove(remove)
	}
	if entry != nil {
		db.tracker.forget(entry)
	}
	return nil
}

// dropRemove takes one scheduled removal off the schedule: only the instance's own removal is dropped, the
// removals its remove cascade scheduled for its children stay (spec/unit-of-work.md "Clarifications on detach and merge").
func (u *unitOfWork) dropRemove(scheduled *scheduledRemove) {
	remaining := u.removes[:0:0]
	for _, candidate := range u.removes {
		if candidate != scheduled {
			remaining = append(remaining, candidate)
		}
	}
	u.removes = remaining
	delete(u.removeOf, scheduled.ptr)
}

// Merge brings a detached copy's state back and returns the session's instance for its key: the
// tracked one when the key is known, otherwise the row read now (one visible round trip; no row is
// CRUD-001) and tracked. Every mapped non-key value of the copy is written onto that instance — the
// version included, so a stale copy still fails its next update's version check (CRUD-010) — which is
// dirty where the values differed. Navigations are not merged and the copy itself stays untracked. An
// instance without a complete key is CRUD-002 (new instances are persisted, not merged); merging the
// session's own instance is a no-op that returns it. UOW-003 on an untracked session.
func Merge[T any](ctx context.Context, db *Db, entity *T) (*T, error) {
	if err := db.requireUnitOfWork("Merge"); err != nil {
		return nil, err
	}
	if entity == nil {
		return nil, core.NewError("CRUD-002", reflect.TypeFor[T]().Name(), "merge needs an instance, got nil")
	}
	// The session's own instance merges as a no-op before any key check.
	if db.entryOf(entity) != nil {
		return entity, nil
	}

	// Merge needs a table-backed, keyed entity — CRUD-003/CRUD-002, decided here at merge time (a view's copy cannot
	// be merged). A scheduled-but-untracked instance is an ordinary copy, and a tracked target scheduled for removal
	// still takes the copy's values (spec/unit-of-work.md "Clarifications on detach and merge").
	entityType := reflect.TypeFor[T]()
	m, err := requireWritableKeyedType(db, entityType)
	if err != nil {
		return nil, err
	}
	keyValues, err := completeKey(m, entity)
	if err != nil {
		return nil, err
	}

	target, err := mergeTarget(ctx, db, m, entityType, entity, keyValues)
	if err != nil {
		return nil, err
	}
	if err := mergeValues(m, target, entity); err != nil {
		return nil, err
	}
	return target.Interface().(*T), nil
}

// mergeTarget is the session's instance for the copy's key: the tracked one, or the row read now and
// registered (CRUD-001 when there is none).
func mergeTarget(ctx context.Context, db *Db, m *core.EntityMap, entityType reflect.Type, entity any, keyValues []any) (reflect.Value, error) {
	key := core.KeyTuple(keyValues)
	if entry := db.tracker.find(m.Type, key); entry != nil {
		return entry.ptr, nil
	}
	fresh, err := readRowByKey(ctx, db, m, entityType, keyValues)
	if err != nil {
		return reflect.Value{}, err
	}
	if !fresh.IsValid() {
		return reflect.Value{}, core.Errorf("CRUD-001", entityType.Name(), "merge found no row with key (%s)", formatKey(keyValues))
	}
	if _, err := db.tracker.register(m, fresh, key); err != nil {
		return reflect.Value{}, err
	}
	return fresh, nil
}

// completeKey is the copy's key as validated key values, or CRUD-002: every part must be set. A null
// part is unset, and so is the zero value of a key the library generates — an unsaved instance of a
// database-generated or client-GUID key has no identity to merge by.
func completeKey(m *core.EntityMap, entity any) ([]any, error) {
	values, err := m.KeyValues(entity)
	if err != nil {
		return nil, err
	}
	for i, value := range values {
		if value == nil || (m.KeyStrategy != core.KeyNatural && isZeroKeyPart(value)) {
			// "Unset key": null, a database-generated key at its default, or an empty client-generated GUID; a natural
			// key's default value is a key (spec/unit-of-work.md "Clarifications on detach and merge").
			return nil, core.Errorf("CRUD-002", m.EntityName(),
				"key part %d (%s) is not set; merge needs a complete key — persist new instances instead", i, m.KeyProperties[i].PropertyName)
		}
	}
	return validateKey(m, values)
}

func isZeroKeyPart(value any) bool {
	return reflect.ValueOf(value).IsZero()
}

// mergeValues writes every mapped non-key value of source (the detached copy) onto target (the
// session's instance), owned types copied whole so an all-null owned type clears a nullable one;
// navigations are not mapped values and stay untouched.
func mergeValues(m *core.EntityMap, target reflect.Value, source any) error {
	targetStruct := target.Elem()
	sourceStruct := reflect.ValueOf(source).Elem()
	for _, owned := range m.OwnedTypes {
		field := sourceStruct.FieldByIndex(owned.Index)
		copied := reflect.New(field.Type()).Elem()
		if field.Kind() == reflect.Pointer {
			if !field.IsNil() {
				duplicate := reflect.New(field.Type().Elem())
				duplicate.Elem().Set(field.Elem())
				copied.Set(duplicate)
			}
		} else {
			copied.Set(field)
		}
		targetStruct.FieldByIndex(owned.Index).Set(copied)
	}
	for _, p := range m.Properties {
		if p.Owner != nil || p.IsKey {
			continue
		}
		value := p.Get(source)
		if bytes, ok := value.([]byte); ok {
			value = append([]byte{}, bytes...) // the copy's slice is not shared with the session's instance
		}
		if err := p.Set(target.Interface(), value); err != nil {
			return err
		}
	}
	return nil
}
