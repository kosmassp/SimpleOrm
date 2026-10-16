package testsupport

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// RecordingDialect decorates the SQLite dialect and records what the four write
// statements were asked to render, in order, so a tracking case's expectWritten,
// a flush's expectStatements, and the unit-of-work tests are observable through
// the dialect seam: where every implementation renders SQL (spec/session.md,
// spec/unit-of-work.md). Everything else delegates. The one recording dialect of
// the port (CODING-STANDARD §8): the conformance runners and the orm tests share it.
type RecordingDialect struct {
	orm.Dialect
	Statements []RenderedStatement
}

// RenderedStatement is one statement the dialect was asked to render: Op is
// insert, update, or delete; for an update, All=true is the full-row UpdateSQL,
// otherwise Properties are the names the column-list UpdateOnlySQL was given.
type RenderedStatement struct {
	Op         string
	Entity     string
	All        bool
	Properties []string
}

// NewRecordingDialect wraps a fresh SQLite dialect.
func NewRecordingDialect() *RecordingDialect {
	return &RecordingDialect{Dialect: sqlite.New()}
}

// Reset forgets what was recorded so far.
func (d *RecordingDialect) Reset() { d.Statements = nil }

// Ops lists the recorded statements as "op Entity" strings, in order — the
// compact form unit-of-work tests compare.
func (d *RecordingDialect) Ops() []string {
	ops := make([]string, len(d.Statements))
	for i, statement := range d.Statements {
		ops[i] = statement.Op + " " + statement.Entity
	}
	return ops
}

func (d *RecordingDialect) InsertSQL(m *orm.EntityMap) string {
	d.Statements = append(d.Statements, RenderedStatement{Op: "insert", Entity: m.EntityName()})
	return d.Dialect.InsertSQL(m)
}

func (d *RecordingDialect) UpdateSQL(m *orm.EntityMap) string {
	d.Statements = append(d.Statements, RenderedStatement{Op: "update", Entity: m.EntityName(), All: true})
	return d.Dialect.UpdateSQL(m)
}

func (d *RecordingDialect) UpdateOnlySQL(m *orm.EntityMap, properties []*orm.PropertyMap) string {
	names := make([]string, len(properties))
	for i, p := range properties {
		names[i] = p.PropertyName
	}
	d.Statements = append(d.Statements, RenderedStatement{Op: "update", Entity: m.EntityName(), Properties: names})
	return d.Dialect.UpdateOnlySQL(m, properties)
}

func (d *RecordingDialect) DeleteSQL(m *orm.EntityMap, checkVersion bool) string {
	d.Statements = append(d.Statements, RenderedStatement{Op: "delete", Entity: m.EntityName()})
	return d.Dialect.DeleteSQL(m, checkVersion)
}

// Updates is the update statements among the recorded ones (what expectWritten judges).
func (d *RecordingDialect) Updates() []RenderedStatement {
	var updates []RenderedStatement
	for _, statement := range d.Statements {
		if statement.Op == "update" {
			updates = append(updates, statement)
		}
	}
	return updates
}
