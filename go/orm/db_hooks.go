package orm

import (
	"reflect"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The declared hooks in the session (Level 3 M3, ADR-0034 §7c, spec/hooks.md): audit columns and soft
// delete are metadata the session acts on, never registered callbacks. One implementation serves the
// immediate operations and the unit of work's flush, because both run the same generated CRUD
// (insertRow, updateEntity, UpdateOnly, deleteEntity). Reads never filter on the soft-delete flag.

// now is the session clock (spec/hooks.md "The session clock"): Options.Clock, the wall clock by
// default. An instant whose zone offset is not zero is VAL-020 — the §7.9 UTC rule.
func (db *Db) now() (time.Time, error) {
	clock := db.options.Clock
	if clock == nil {
		return time.Now().UTC(), nil
	}
	instant := clock()
	// "UTC" is the language's notion of it (spec/hooks.md Clarifications): in Go a zero zone offset — a fixed zone of
	// offset zero is UTC, time.Local on a UTC machine passes — and any other offset is VAL-020, at the write that reads the clock.
	if _, offset := instant.Zone(); offset != 0 {
		return time.Time{}, core.Errorf("VAL-020", "Options.Clock",
			"the session clock returned %s, which is not UTC; return an instant in UTC (time.Now().UTC())",
			instant.Format(time.RFC3339))
	}
	return instant.UTC(), nil
}

// fillCreatedAt is the [CreatedAt] hook (spec/hooks.md): on insert the column takes the clock when its
// value is unset — nil, or the zero instant — and keeps a value the caller set. It writes the value
// onto the entity, so the instance reads back what the row holds, and returns the function that puts the
// previous value back when the insert then fails (a failed write leaves the instance as it was, spec/hooks.md
// Clarifications).
func (db *Db) fillCreatedAt(m *core.EntityMap, entity any) (func(), error) {
	property := m.CreatedAtProperty
	if property == nil {
		return func() {}, nil
	}
	prior := property.Get(entity)
	if !isUnsetInstant(prior) {
		return func() {}, nil
	}
	instant, err := db.now()
	if err != nil {
		return nil, err
	}
	if err := property.Set(entity, instant); err != nil {
		return nil, err
	}
	return func() { _ = property.Set(entity, prior) }, nil // the value came from Get, so it always assigns back
}

// isUnsetInstant is "null, or the type's default instant" (spec/hooks.md "[CreatedAt]").
func isUnsetInstant(value any) bool {
	if value == nil {
		return true
	}
	instant, ok := value.(time.Time)
	return ok && instant.IsZero()
}

// stampSet is the [UpdatedAt] hook (spec/hooks.md): the library is the column's writer, so every write
// that changes a row writes the clock — the column joins the SET after the caller's columns (when not
// already listed) and the value lands on the entity. It returns the (possibly extended) set and a
// function putting the entity's previous value back when the statement then fails. An entity with no
// updated-at column gets its set back untouched.
func (db *Db) stampSet(m *core.EntityMap, entity any, set []*core.PropertyMap) ([]*core.PropertyMap, func(), error) {
	property := m.UpdatedAtProperty
	if property == nil {
		return set, func() {}, nil
	}
	// The clock is read per write (spec/hooks.md Clarifications), so a flush of several rows may stamp different instants.
	instant, err := db.now()
	if err != nil {
		return nil, nil, err
	}
	// A failed write leaves the instance as it was (spec/hooks.md Clarifications): the caller puts the stamp back with restore.
	prior := property.Get(entity)
	if err := property.Set(entity, instant); err != nil {
		return nil, nil, err
	}
	restore := func() { _ = property.Set(entity, prior) } // the value came from Get, so it always assigns back

	for _, listed := range set {
		if listed == property {
			return set, restore, nil
		}
	}
	// Listing the stamp column in a column-list update changes nothing: it takes the clock and is written once
	// (spec/hooks.md Clarifications).
	return append(append([]*core.PropertyMap{}, set...), property), restore, nil
}

// isWritableChange reports whether a changed column is one a tracked update writes: key, version, and
// generated values never are, and the [UpdatedAt] column is never what makes an instance dirty — the
// session writes it, and only when something else changed (spec/hooks.md "[UpdatedAt]").
func isWritableChange(p *core.PropertyMap) bool {
	return !p.IsKey && !p.IsVersion && !p.IsGenerated && p.Audit != core.AuditUpdatedAt
}

// prepareSoftDeleteEntity is the entity form of a delete on a [SoftDelete] entity (spec/hooks.md): the
// column-list update that sets the flag true with the [UpdatedAt] stamp and the version rules of an
// update — CRUD-010 on a stale version (the version bumps on success), CRUD-001 on zero rows without
// one. The flag and the stamp are set on the instance; a failed statement puts them back.
func (db *Db) prepareSoftDeleteEntity(entityType reflect.Type, m *core.EntityMap, entity any) (*preparedWrite, error) {
	flag := m.SoftDeleteProperty
	priorFlag := flag.Get(entity)
	if err := flag.Set(entity, true); err != nil {
		return nil, err
	}
	set, restoreStamp, err := db.stampSet(m, entity, []*core.PropertyMap{flag})
	if err != nil {
		_ = flag.Set(entity, priorFlag) // the value came from Get, so it always assigns back
		return nil, err
	}
	write, err := db.countedWrite(entityType, m, entity, "delete", db.columnListWrite(m, set))
	if err != nil {
		_ = flag.Set(entity, priorFlag)
		restoreStamp()
		return nil, err
	}
	write.undoPrepare = func() {
		_ = flag.Set(entity, priorFlag)
		restoreStamp()
	}
	judge := db.updateJudge(entityType, m, entity)
	write.finish = func(result writeResult, _ *undoJournal) error { return judge(result.affected) }
	return write, nil
}

// prepareSoftDeleteByKey is the by-key form (spec/hooks.md): the same column-list update, keyed. Delete has
// already refused a versioned entity (CRUD-012) — the version it does not know cannot be bumped
// blindly. There is no instance to carry the values, so a scratch one holds the key, the flag, and the
// stamp for the one update path to bind; zero rows is CRUD-001.
func (db *Db) prepareSoftDeleteByKey(entityType reflect.Type, m *core.EntityMap, keyValues []any) (*preparedWrite, error) {
	scratch := reflect.New(entityType).Interface()
	for i, key := range m.KeyProperties {
		if err := key.Set(scratch, keyValues[i]); err != nil {
			return nil, err
		}
	}
	flag := m.SoftDeleteProperty
	if err := flag.Set(scratch, true); err != nil {
		return nil, err
	}
	set, _, err := db.stampSet(m, scratch, []*core.PropertyMap{flag})
	if err != nil {
		return nil, err
	}
	write, err := db.countedWrite(entityType, m, scratch, "delete", db.columnListWrite(m, set))
	if err != nil {
		return nil, err
	}
	write.instance = nil // the scratch instance is the session's, not the caller's: rule 1a has no instance to keep apart
	judge := db.updateJudge(entityType, m, scratch)
	write.finish = func(result writeResult, _ *undoJournal) error { return judge(result.affected) }
	return write, nil
}
