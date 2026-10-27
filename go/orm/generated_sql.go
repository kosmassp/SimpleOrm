package orm

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The session's cache of generated CRUD text (ADR-0048 item 1): the INSERT, the full-row UPDATE, both DELETE forms,
// the key read, the select-all, and each column-list UPDATE by its ordered column set, with the property lists they
// bind and the labels and parameter names each bind uses (item 3). Keyed by the *core.EntityMap instance — the
// session's loader owns its maps, and the session has one dialect, so the pair (map, dialect) is the key by
// identity, never a string that could collide across models. The dialect's renderings are pure functions of their
// arguments (the seam's contract), so the first rendering is every rendering. Guarded by one mutex: a session is
// one connection, but nothing here relies on that.
//
// The step-script runners used to observe these renderings by wrapping the dialect; the dialect is now asked once
// per text, so the session tells a dialect implementing core.RenderObserver about every rendering it takes, cached
// or not, at the moment it would have asked (spec/session.md "The seam").

// maxColumnLists caps the cached column-list updates per map: beyond it a set renders uncached, as before.
const maxColumnLists = 256

// boundText is one generated statement with the properties it binds, in statement order, and the label each bind
// names in a conversion error ("<Entity>.<Property>").
type boundText struct {
	sql    string
	bound  []*core.PropertyMap
	labels []string
}

// generatedSQL is one entity map's cached text.
type generatedSQL struct {
	m      *core.EntityMap
	index  map[*core.PropertyMap]int // a property's position in m.Properties: the column-list key
	labels []string                  // parallel to m.Properties: "<Entity>.<Property>"

	insert          *boundText
	insertNoRow     string
	update          *boundText
	deleteByKey     string
	deleteVersioned *boundText // the entity form, version-checked when the map has a version
	deleteEntity    *boundText
	keyRead         string
	keyNames        []string // "k0", "k1", …: the key read's parameter names
	keyLabels       []string // "<Entity> key[i]": the key read's and the by-key delete's labels
	selectAll       string
	columnLists     map[string]*boundText
}

// renderCache is the per-session holder.
type renderCache struct {
	mu      sync.Mutex
	entries map[*core.EntityMap]*generatedSQL
}

// generatedFor returns m's entry, creating it; the caller holds mu.
func (c *renderCache) generatedFor(m *core.EntityMap) *generatedSQL {
	if g, ok := c.entries[m]; ok {
		return g
	}
	if c.entries == nil {
		c.entries = map[*core.EntityMap]*generatedSQL{}
	}
	g := &generatedSQL{m: m, index: make(map[*core.PropertyMap]int, len(m.Properties)), labels: make([]string, len(m.Properties))}
	name := m.Type.Name()
	for i, p := range m.Properties {
		g.index[p] = i
		g.labels[i] = name + "." + p.PropertyName
	}
	g.keyNames = make([]string, len(m.KeyProperties))
	g.keyLabels = make([]string, len(m.KeyProperties))
	for i := range m.KeyProperties {
		g.keyNames[i] = fmt.Sprintf("k%d", i)
		g.keyLabels[i] = fmt.Sprintf("%s key[%d]", name, i)
	}
	c.entries[m] = g
	return g
}

// bind builds a boundText over properties of g's map.
func (g *generatedSQL) bind(sqlText string, properties []*core.PropertyMap) *boundText {
	labels := make([]string, len(properties))
	for i, p := range properties {
		if at, ok := g.index[p]; ok {
			labels[i] = g.labels[at]
		} else {
			labels[i] = g.m.Type.Name() + "." + p.PropertyName
		}
	}
	return &boundText{sql: sqlText, bound: properties, labels: labels}
}

// label is bind i's label for a write over entityType: the cached one when entityType is the map's own type (always,
// in practice — the loader keys maps by the struct type), else built as the call sites always built it.
func (t *boundText) label(i int, entityType reflect.Type, m *core.EntityMap) string {
	if entityType == m.Type {
		return t.labels[i]
	}
	return entityType.Name() + "." + t.bound[i].PropertyName
}

// observeWrite tells the session's render observer, if its dialect is one, about a write rendering the session takes.
func (db *Db) observeWrite(op string, m *core.EntityMap, all bool, properties []*core.PropertyMap) {
	if db.renderObserver != nil {
		db.renderObserver.ObserveWriteRendering(op, m, all, properties)
	}
}

// generated returns m's entry with the cache locked; the caller unlocks.
func (db *Db) generated(m *core.EntityMap) *generatedSQL {
	db.renders.mu.Lock()
	return db.renders.generatedFor(m)
}

// insertText is the generated INSERT and its bound non-generated properties.
func (db *Db) insertText(m *core.EntityMap) (*boundText, string) {
	g := db.generated(m)
	defer db.renders.mu.Unlock()
	if g.insert == nil {
		var bound []*core.PropertyMap
		for _, p := range m.Properties {
			if !p.IsGenerated {
				bound = append(bound, p)
			}
		}
		g.insert = g.bind(db.options.Dialect.InsertSQL(m), bound)
		g.insertNoRow = fmt.Sprintf("insert into %s: RETURNING produced no row", m.RelationName)
	}
	return g.insert, g.insertNoRow
}

// updateText is the full-row UPDATE: SET values, key values, and the current version for the WHERE; database-generated
// non-key columns are never written.
func (db *Db) updateText(m *core.EntityMap) *boundText {
	g := db.generated(m)
	defer db.renders.mu.Unlock()
	if g.update == nil {
		var bound []*core.PropertyMap
		for _, p := range m.Properties {
			if p.IsKey || p.IsVersion || !p.IsGenerated {
				bound = append(bound, p)
			}
		}
		g.update = g.bind(db.options.Dialect.UpdateSQL(m), bound)
	}
	return g.update
}

// updateOnlyText is the column-list UPDATE for set (in list order) with its bindings: the SET properties, then the key,
// then the version for the WHERE. Cached by the ordered column set; a set beyond the cap renders uncached.
func (db *Db) updateOnlyText(m *core.EntityMap, set []*core.PropertyMap) *boundText {
	g := db.generated(m)
	defer db.renders.mu.Unlock()

	var buffer [64]byte
	key := buffer[:0]
	for _, p := range set {
		at, ok := g.index[p]
		if !ok {
			return g.bind(db.options.Dialect.UpdateOnlySQL(m, set), updateOnlyBindings(m, set))
		}
		key = append(key, byte(at>>8), byte(at))
	}
	if cached, ok := g.columnLists[string(key)]; ok {
		return cached
	}
	text := g.bind(db.options.Dialect.UpdateOnlySQL(m, set), updateOnlyBindings(m, set))
	if len(g.columnLists) < maxColumnLists {
		if g.columnLists == nil {
			g.columnLists = map[string]*boundText{}
		}
		g.columnLists[string(key)] = text // updateOnlyBindings built a fresh list: the caller's set is never aliased
	}
	return text
}

// deleteByKeyText is the DELETE by key, unversioned, with the key labels.
func (db *Db) deleteByKeyText(m *core.EntityMap) (string, []string) {
	g := db.generated(m)
	defer db.renders.mu.Unlock()
	if g.deleteByKey == "" {
		g.deleteByKey = db.options.Dialect.DeleteSQL(m, false)
	}
	return g.deleteByKey, g.keyLabels
}

// deleteEntityText is the entity form of the DELETE: by key, and by version when checkVersion.
func (db *Db) deleteEntityText(m *core.EntityMap, checkVersion bool) *boundText {
	g := db.generated(m)
	defer db.renders.mu.Unlock()
	cached := &g.deleteEntity
	if checkVersion {
		cached = &g.deleteVersioned
	}
	if *cached == nil {
		bound := append([]*core.PropertyMap{}, m.KeyProperties...)
		if checkVersion {
			bound = append(bound, m.VersionProperty)
		}
		*cached = g.bind(db.options.Dialect.DeleteSQL(m, checkVersion), bound)
	}
	return *cached
}

// keyReadText is the generated select by key with its parameter names and labels.
func (db *Db) keyReadText(m *core.EntityMap) (string, []string, []string) {
	g := db.generated(m)
	defer db.renders.mu.Unlock()
	if g.keyRead == "" {
		dialect := db.options.Dialect
		predicates := make([]string, len(m.KeyProperties))
		for i, key := range m.KeyProperties {
			predicates[i] = dialect.QuoteIdentifier(key.ColumnName) + " = @" + g.keyNames[i]
		}
		g.keyRead = "select " + quotedColumns(dialect, m.Properties) +
			" from " + dialect.QuoteIdentifier(m.RelationName) +
			" where " + strings.Join(predicates, " and ")
	}
	return g.keyRead, g.keyNames, g.keyLabels
}

// selectAllText is the generated select-all: explicit column list, ordered by the key when one exists.
func (db *Db) selectAllText(m *core.EntityMap) string {
	g := db.generated(m)
	defer db.renders.mu.Unlock()
	if g.selectAll == "" {
		dialect := db.options.Dialect
		text := "select " + quotedColumns(dialect, m.Properties) + " from " + dialect.QuoteIdentifier(m.RelationName)
		if len(m.KeyProperties) > 0 {
			text += " order by " + quotedColumns(dialect, m.KeyProperties)
		}
		g.selectAll = text
	}
	return g.selectAll
}

// quotedColumns is the properties' quoted column names, comma-separated.
func quotedColumns(dialect core.Dialect, properties []*core.PropertyMap) string {
	columns := make([]string, len(properties))
	for i, p := range properties {
		columns[i] = dialect.QuoteIdentifier(p.ColumnName)
	}
	return strings.Join(columns, ", ")
}
