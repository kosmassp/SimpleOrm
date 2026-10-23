package testsupport

import (
	"context"
	"database/sql"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
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
	// Messages is the size of every message a flush or an explicit batch formed since Reset, in order: the test hook of
	// spec/session.md "The step scripts" (the message boundary is the session's, so the recorder implements the
	// session's optional core.MessageObserver, never a seam member).
	Messages []int
	// OneRoundTrip makes the dialect behave as one that batches (ADR-0044): SupportsStatementBatching reports true and a
	// planned message is handed to SendMessage — SequentialMessage, the statements run in order on the real SQLite
	// database with every result judged afterwards — instead of running one statement at a time. The conformance
	// runners replay the batch and flush cases both ways: the plan is the same, and so is every expectation.
	OneRoundTrip bool
}

// ObserveMessage records one message's statement count (core.MessageObserver).
func (d *RecordingDialect) ObserveMessage(statements int) {
	d.Messages = append(d.Messages, statements)
}

// SupportsStatementBatching is false unless the dialect was asked to behave as a batching one (OneRoundTrip).
func (d *RecordingDialect) SupportsStatementBatching() bool { return d.OneRoundTrip }

// SendMessage is the core.StatementBatcher capability; the session asks for it only where OneRoundTrip says so.
func (d *RecordingDialect) SendMessage(ctx context.Context, _ *sql.Conn, exec core.SQLExecutor, statements []core.MessageStatement) ([]core.MessageResult, error) {
	return SequentialMessage(ctx, exec, statements)
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
func (d *RecordingDialect) Reset() {
	d.Statements = nil
	d.Messages = nil
}

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
