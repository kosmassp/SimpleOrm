package core

import (
	"context"
	"database/sql"
)

// Statement batching, the dialect side (Level 4 item 3, ADR-0044, spec/session.md "Batching", spec/dialects.md
// "Statement batching"). The plan — which statements share a message — is the session's and the same on every
// dialect; what a dialect contributes is the two seam flags (Dialect.SupportsStatementBatching,
// Dialect.MaxParametersPerMessage) and, where its driver can send a planned message as one round trip, the optional
// StatementBatcher capability below. A dialect without it (SQLite) runs the plan's messages one statement at a time.

// SQLExecutor is the surface *sql.Conn and *sql.Tx share: every statement runs on it, so it runs inside the
// session's transaction when one is active (§7.17).
type SQLExecutor interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// StatementResult says what the session reads back from one statement of a message.
type StatementResult int

const (
	// ResultNone: nothing is read (an insert whose key the caller supplies); only an error matters.
	ResultNone StatementResult = iota
	// ResultKey: the first column of the first row of the first result that has columns — the generated key
	// (`returning <key>`, SQL Server's `select cast(scope_identity() as bigint)`), as an integer.
	ResultKey
	// ResultReportedCount: the same scalar, but it is the statement's own affected-row count
	// (Dialect.ReportsAffectedRowsBySelect: SQL Server's `select @@rowcount`).
	ResultReportedCount
	// ResultDriverCount: the driver's affected-row count for this statement — a generated update or delete
	// where the dialect does not report by select, and every registry command.
	ResultDriverCount
)

// MessageStatement is one statement of a planned message: its SQL with the `@name` placeholders the session
// bound (as written for the dialect), its arguments (every element a sql.NamedArg), and the result to read.
type MessageStatement struct {
	SQL    string
	Args   []any
	Result StatementResult
	// Name is the statement's statement-log name (`User.insert`, `command`; spec/session.md "Statement log").
	Name string
}

// MessageResult is one statement's result. Key holds the scalar of a ResultKey statement (an integer, widened to
// int64); Affected the count of a ResultReportedCount or ResultDriverCount statement.
type MessageResult struct {
	Key      any
	Affected int64
}

// StatementBatcher is the optional dialect capability that sends a planned message as one round trip and hands
// back each statement's result (ADR-0044). It is not a Dialect member (the seam keeps the reference's member set,
// §7.25): a dialect whose driver cannot do this — SQLite — simply lacks it, and the session runs the message
// sequentially. Dialect.SupportsStatementBatching reports it, and the session asks for both.
//
// SendMessage runs the statements in order on conn (the session's pinned connection; exec is its current
// executor, the session transaction when one is active) and returns one result per statement. A realization may
// use more round trips than one — a statement its driver cannot carry in a message goes on its own — never fewer,
// and never reorders. Every statement has run when it returns without error; on error some may have run (a
// pipelined or multi-statement message does not stop at a statement that merely affects no rows, and a failing one
// may leave its neighbours run or skipped by the engine) and the session fails the call, which is atomic through the
// transaction.
//
// The statement log (ADR-0045) is the realization's to write: the session cannot know the text a dialect finally
// sends (SQL Server renames each statement's placeholders apart). A realization therefore writes one line per
// statement, in statement order, to StatementLogFrom(ctx) — each with its statement's Name, its SQL and its parameters
// exactly as that realization sends them — before the message goes out; with no log on ctx it writes nothing.
type StatementBatcher interface {
	SendMessage(ctx context.Context, conn *sql.Conn, exec SQLExecutor, statements []MessageStatement) ([]MessageResult, error)
}

// MessageObserver is the test hook through which a conformance runner observes the size of every message a flush
// or an explicit batch formed (spec/session.md "The step scripts": a way for each implementation's runner to see
// the message boundary, never public API). A dialect decorator that implements it is told, once per message and in
// order, how many statements it carried — registry commands included. The session type-asserts its dialect for it.
type MessageObserver interface {
	ObserveMessage(statements int)
}
