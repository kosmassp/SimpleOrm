package orm

import (
	"bytes"
	"reflect"
	"sort"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// tracker is the identity map of a tracked session (Level 3 M1, ADR-0034
// add.1, spec/session.md "Tracking"): one instance per (entity type, key) on
// the §7.4 identity, plus a snapshot of each instance's mapped values as read
// or last written. Per type the entries stay sorted by key and are found by
// binary search through core.KeyTuple.Compare — the one value-wise comparator
// (decimals numerically, temporals by instant, bytes by content), never a
// second equality. A session is one connection and not safe for concurrent
// use, so the tracker carries no lock.
type tracker struct {
	types map[reflect.Type][]*trackedEntry // struct type -> entries sorted by key
	byPtr map[any]*trackedEntry            // the tracked *E -> its entry
}

// trackedEntry is one tracked instance: the pointer the caller holds, the map
// it was tracked under, the key it was registered with (identity — the live
// key fields may have been edited since, which a write refuses, UOW-002), and
// the snapshot, parallel to m.Properties.
type trackedEntry struct {
	ptr      reflect.Value // *E
	m        *core.EntityMap
	key      core.KeyTuple
	snapshot []any
}

func newTracker() *tracker {
	return &tracker{types: map[reflect.Type][]*trackedEntry{}, byPtr: map[any]*trackedEntry{}}
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
// key, are dropped first: a pointer has one identity and a key one instance.
func (tr *tracker) register(m *core.EntityMap, ptr reflect.Value, key core.KeyTuple) *trackedEntry {
	if old := tr.byPtr[ptr.Interface()]; old != nil {
		tr.forget(old)
	}
	if holder := tr.find(m.Type, key); holder != nil {
		tr.forget(holder)
	}
	entry := &trackedEntry{ptr: ptr, m: m, key: key, snapshot: takeSnapshot(m, ptr.Interface())}
	i, _ := tr.search(m.Type, key)
	entries := tr.types[m.Type]
	entries = append(entries, nil)
	copy(entries[i+1:], entries[i:])
	entries[i] = entry
	tr.types[m.Type] = entries
	tr.byPtr[ptr.Interface()] = entry
	return entry
}

// forget removes entry from both indexes.
func (tr *tracker) forget(entry *trackedEntry) {
	delete(tr.byPtr, entry.ptr.Interface())
	if i, found := tr.search(entry.m.Type, entry.key); found && tr.types[entry.m.Type][i] == entry {
		entries := tr.types[entry.m.Type]
		tr.types[entry.m.Type] = append(entries[:i], entries[i+1:]...)
	}
}

// takeSnapshot copies every mapped value of entity, in map order (owned
// members flat, like their columns — ADR-0030). Reference-like values are
// deep-copied so an in-place edit (a []byte element, a JSON-handler value)
// shows as a change.
func takeSnapshot(m *core.EntityMap, entity any) []any {
	snapshot := make([]any, len(m.Properties))
	for i, p := range m.Properties {
		snapshot[i] = cloneValue(p.Get(entity))
	}
	return snapshot
}

// changed lists the mapped properties whose current value differs from the
// snapshot, in map order.
func (e *trackedEntry) changed() []*core.PropertyMap {
	entity := e.ptr.Interface()
	var changed []*core.PropertyMap
	for i, p := range e.m.Properties {
		if !sameValue(e.snapshot[i], p.Get(entity)) {
			changed = append(changed, p)
		}
	}
	return changed
}

// resnapshot moves the snapshot of the listed properties to their current
// values — what a write just stored. Other properties keep their snapshot, so
// a local change that was not written stays dirty.
func (e *trackedEntry) resnapshot(properties []*core.PropertyMap) {
	entity := e.ptr.Interface()
	for _, p := range properties {
		for i, candidate := range e.m.Properties {
			if candidate == p {
				e.snapshot[i] = cloneValue(p.Get(entity))
				break
			}
		}
	}
}

// sameValue is snapshot equality: value-wise for the fixed table's types
// through core.CompareValues (decimals numerically, temporals by instant),
// by content for byte arrays, and deep equality for anything else (a
// handler's JSON value).
func sameValue(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch av := a.(type) {
	case []byte:
		bv, ok := b.([]byte)
		return ok && bytes.Equal(av, bv)
	case time.Time, core.Decimal, core.GUID,
		string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		if reflect.TypeOf(a) != reflect.TypeOf(b) {
			return false
		}
		return core.CompareValues(a, b) == 0
	}
	return reflect.DeepEqual(a, b)
}

// cloneValue deep-copies a snapshot value; immutable scalars (and time.Time,
// Decimal, GUID, which are values) are returned as they are.
func cloneValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return append([]byte{}, x...)
	case time.Time, core.Decimal, core.GUID, string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return v
	}
	return deepCopy(reflect.ValueOf(v)).Interface()
}

// deepCopy copies slices, maps, pointers, and the exported fields of structs;
// everything else is a plain value copy.
func deepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		copied := reflect.New(v.Type().Elem())
		copied.Elem().Set(deepCopy(v.Elem()))
		return copied
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		copied := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			copied.Index(i).Set(deepCopy(v.Index(i)))
		}
		return copied
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		copied := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			copied.SetMapIndex(iter.Key(), deepCopy(iter.Value()))
		}
		return copied
	case reflect.Struct:
		copied := reflect.New(v.Type()).Elem()
		copied.Set(v) // unexported fields copy shallowly
		for i := 0; i < v.NumField(); i++ {
			if copied.Field(i).CanSet() {
				copied.Field(i).Set(deepCopy(v.Field(i)))
			}
		}
		return copied
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		copied := reflect.New(v.Type()).Elem()
		copied.Set(deepCopy(v.Elem()))
		return copied
	default:
		return v
	}
}
