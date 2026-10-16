package orm

import (
	"bytes"
	"reflect"
	"sort"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
)

// tracker is the identity map of a tracked session (Level 3 M1, ADR-0034
// add.1, spec/session.md "Tracking"): one instance per (entity type, key) on
// the §7.4 identity, plus a snapshot of each instance's mapped values as read
// or last written. Per type the entries stay sorted by key and are found by
// binary search through core.KeyTuple.Compare — the one value-wise comparator
// (decimals numerically, temporals by instant, bytes by content), never a
// second equality. A snapshot holds the database-bound form of each value —
// the type converter's output (spec/session.md Clarifications, ADR-0035) — so
// "dirty" means "the database would receive a different value". A session is
// one connection and not safe for concurrent use, so the tracker carries no lock.
type tracker struct {
	converter *mapping.TypeConverter           // the one conversion every write binds through
	types     map[reflect.Type][]*trackedEntry // struct type -> entries sorted by key
	byPtr     map[any]*trackedEntry            // the tracked *E -> its entry
}

// trackedEntry is one tracked instance: the pointer the caller holds, the map
// it was tracked under, the key it was registered with (identity — the live
// key fields may have been edited since, which a write refuses, UOW-002), and
// the snapshot (bound values), parallel to m.Properties.
type trackedEntry struct {
	ptr      reflect.Value // *E
	m        *core.EntityMap
	key      core.KeyTuple
	snapshot []any
}

func newTracker(converter *mapping.TypeConverter) *tracker {
	return &tracker{converter: converter, types: map[reflect.Type][]*trackedEntry{}, byPtr: map[any]*trackedEntry{}}
}

// search is the insertion point for key in t's sorted entries and whether an
// entry with an equal key sits there.
func (tr *tracker) search(t reflect.Type, key core.KeyTuple) (int, bool) {
	entries := tr.types[t]
	i := sort.Search(len(entries), func(i int) bool { return entries[i].key.Compare(key) >= 0 })
	return i, i < len(entries) && entries[i].key.Compare(key) == 0
}

// find returns the instance tracked for (t, key), or nil.
func (tr *tracker) find(t reflect.Type, key core.KeyTuple) *trackedEntry {
	i, found := tr.search(t, key)
	if !found {
		return nil
	}
	return tr.types[t][i]
}

// register tracks ptr (a *E) under key with a fresh snapshot. The pointer's
// previous entry (it was tracked under another key), and a stale holder of
// key, are dropped first: a pointer has one identity and a key one instance
// (a key reused after an external delete: the instance that wrote the row wins).
func (tr *tracker) register(m *core.EntityMap, ptr reflect.Value, key core.KeyTuple) (*trackedEntry, error) {
	snapshot, err := tr.snapshotOf(m, ptr.Interface())
	if err != nil {
		return nil, err
	}
	if old := tr.byPtr[ptr.Interface()]; old != nil {
		tr.forget(old)
	}
	if holder := tr.find(m.Type, key); holder != nil {
		tr.forget(holder)
	}
	entry := &trackedEntry{ptr: ptr, m: m, key: key, snapshot: snapshot}
	i, _ := tr.search(m.Type, key)
	entries := tr.types[m.Type]
	entries = append(entries, nil)
	copy(entries[i+1:], entries[i:])
	entries[i] = entry
	tr.types[m.Type] = entries
	tr.byPtr[ptr.Interface()] = entry
	return entry, nil
}

// forget removes entry from both indexes.
func (tr *tracker) forget(entry *trackedEntry) {
	delete(tr.byPtr, entry.ptr.Interface())
	if i, found := tr.search(entry.m.Type, entry.key); found && tr.types[entry.m.Type][i] == entry {
		entries := tr.types[entry.m.Type]
		tr.types[entry.m.Type] = append(entries[:i], entries[i+1:]...)
	}
}

// snapshotOf is the bound form of every mapped value of entity, in map order
// (owned members flat, like their columns — ADR-0030).
func (tr *tracker) snapshotOf(m *core.EntityMap, entity any) ([]any, error) {
	snapshot := make([]any, len(m.Properties))
	for i, p := range m.Properties {
		bound, err := tr.bound(m, p, entity)
		if err != nil {
			return nil, err
		}
		snapshot[i] = bound
	}
	return snapshot, nil
}

// bound is the value the database would receive for p — the converter's
// ToDatabase, the one conversion every write binds through. A []byte is
// copied (the converter hands back the instance's own slice); a Decimal the
// converter rendered as its own text stays the Decimal, so two decimals equal
// in value are clean whatever their text; everything else the converter
// returns (strings, numbers, instants) is immutable.
func (tr *tracker) bound(m *core.EntityMap, p *core.PropertyMap, entity any) (any, error) {
	value := p.Get(entity)
	bound, err := tr.converter.ToDatabase(value, p.ColumnType, m.EntityName()+"."+p.PropertyName)
	if err != nil {
		return nil, err
	}
	switch b := bound.(type) {
	case []byte:
		return append([]byte{}, b...), nil
	case string:
		if d, ok := value.(core.Decimal); ok && b == d.String() {
			return d, nil
		}
	}
	return bound, nil
}

// changed lists the mapped properties whose bound value differs from the
// snapshot, in map order.
func (tr *tracker) changed(e *trackedEntry) ([]*core.PropertyMap, error) {
	entity := e.ptr.Interface()
	var changed []*core.PropertyMap
	for i, p := range e.m.Properties {
		current, err := tr.bound(e.m, p, entity)
		if err != nil {
			return nil, err
		}
		if !sameBound(e.snapshot[i], current) {
			changed = append(changed, p)
		}
	}
	return changed, nil
}

// resnapshot moves the snapshot of the listed properties to their current
// values — what a write just stored. Other properties keep their snapshot, so
// a local change that was not written stays dirty.
func (tr *tracker) resnapshot(e *trackedEntry, properties []*core.PropertyMap) error {
	entity := e.ptr.Interface()
	for _, p := range properties {
		for i, candidate := range e.m.Properties {
			if candidate != p {
				continue
			}
			bound, err := tr.bound(e.m, p, entity)
			if err != nil {
				return err
			}
			e.snapshot[i] = bound
			break
		}
	}
	return nil
}

// sameBound compares two bound values: byte arrays by content, decimals
// numerically (core.CompareValues, the one value-wise comparator), instants
// by Equal, every other scalar by value. A handler may bind something
// uncomparable (a map); that falls back to deep equality.
func sameBound(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch av := a.(type) {
	case []byte:
		bv, ok := b.([]byte)
		return ok && bytes.Equal(av, bv)
	case core.Decimal:
		bv, ok := b.(core.Decimal)
		return ok && core.CompareValues(av, bv) == 0
	case time.Time:
		bv, ok := b.(time.Time)
		return ok && av.Equal(bv)
	}
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return false
	}
	if reflect.TypeOf(a).Comparable() {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}
