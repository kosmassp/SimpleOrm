package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// Statement batching on PostgreSQL (Level 4 item 3, ADR-0044, spec/dialects.md "Statement batching"): a planned
// message goes out as one pgx Batch — the statements pipelined on the session's connection, one Sync — and each
// statement's result is read in order. The session reaches it through the optional core.StatementBatcher beside the
// seam, the way SchemaGuard reaches DescribeStatement, and always inside a transaction (its own or the caller's), so
// a pipeline's implicit transaction never comes into play.

var _ core.StatementBatcher = (*Dialect)(nil)

// SendMessage sends the statements as one pgx pipeline and reads their results in order: an insert's `returning`
// key as its first column, every other statement's count from its command tag (pgx's per-queued-query tag). A
// registry command whose SQL holds several statements cannot be carried — the extended protocol refuses more than one
// command in a prepared statement — so it goes on its own through the session's executor, between the pipelines
// of the statements around it: more round trips than the plan's message, never fewer, and in order.
//
// A statement the server rejects fails the pipeline: PostgreSQL skips what follows it up to the Sync. A statement that
// merely affects no rows does not — the statements after it run and the session judges the counts afterwards, so
// inside a caller's transaction the rows of statements after the failing one may be there (spec/session.md
// "Batching", Failure). The cached-plan error of Clarification 15 surfaces as TX-002: a batched call is always in a
// transaction, which PostgreSQL has aborted, so it is never retried.
func (d *Dialect) SendMessage(ctx context.Context, connection *sql.Conn, exec core.SQLExecutor, statements []core.MessageStatement) ([]core.MessageResult, error) {
	results := make([]core.MessageResult, len(statements))
	for start := 0; start < len(statements); {
		end := start
		for end < len(statements) && !needsItsOwnMessage(statements[end]) {
			end++
		}
		if end > start {
			if err := sendPipeline(ctx, connection, statements[start:end], results[start:end]); err != nil {
				return nil, err
			}
			start = end
			continue
		}
		core.StatementLogFrom(ctx).Statement(statements[start].Name, statements[start].SQL, statements[start].Args)
		result, err := sendAlone(ctx, exec, statements[start])
		if err != nil {
			return nil, err
		}
		results[start] = result
		start++
	}
	return results, nil
}

// needsItsOwnMessage: a registry command with several statements in its SQL (pgx's prepare refuses it in a batch).
func needsItsOwnMessage(statement core.MessageStatement) bool {
	return statement.Result == core.ResultDriverCount && hasMultipleStatements(statement.SQL)
}

// sendPipeline queues the statements on a pgx.Batch (placeholders rewritten and arguments arranged as the connection
// does for a single statement), sends it, and reads each result in order.
func sendPipeline(ctx context.Context, connection *sql.Conn, statements []core.MessageStatement, results []core.MessageResult) error {
	return connection.Raw(func(driverConn any) error {
		c, ok := driverConn.(*conn)
		if !ok {
			return fmt.Errorf("the %T driver connection is not a SimpleOrm postgres connection", driverConn)
		}
		batch := &pgx.Batch{}
		for _, statement := range statements {
			rewritten := rewritePlaceholders(statement.SQL)
			arranged, err := rewritten.arrange(namedValues(statement.Args))
			if err != nil {
				return err
			}
			values := make([]any, len(arranged))
			for i, arg := range arranged {
				values[i] = arg.Value
			}
			batch.Queue(rewritten.sql, values...)
		}

		log := core.StatementLogFrom(ctx) // one line per statement, the SQL as the session bound it, before the pipeline goes out
		for _, statement := range statements {
			log.Statement(statement.Name, statement.SQL, statement.Args)
		}
		pending := c.inner.Conn().SendBatch(ctx, batch)
		for i, statement := range statements {
			if err := readResult(pending, statement, &results[i]); err != nil {
				_ = pending.Close() // the failure is the error to report; Close drains the pipeline
				return c.surfaceCachedPlan(statement.SQL, err)
			}
		}
		if err := pending.Close(); err != nil {
			return c.surfaceCachedPlan(statements[len(statements)-1].SQL, err)
		}
		return nil
	})
}

// namedValues turns the session's sql.NamedArg values into the driver's named values, which `arrange` puts in `$n` order.
func namedValues(args []any) []driver.NamedValue {
	values := make([]driver.NamedValue, len(args))
	for i, arg := range args {
		named, ok := arg.(sql.NamedArg)
		if !ok {
			values[i] = driver.NamedValue{Ordinal: i + 1, Value: arg} // arrange refuses a positional argument beside @name placeholders
			continue
		}
		values[i] = driver.NamedValue{Name: named.Name, Ordinal: i + 1, Value: named.Value}
	}
	return values
}

// readResult reads the next statement's result off the pipeline.
func readResult(pending pgx.BatchResults, statement core.MessageStatement, result *core.MessageResult) error {
	switch statement.Result {
	case core.ResultKey, core.ResultReportedCount:
		rows, err := pending.Query()
		if err != nil {
			return err
		}
		if !rows.Next() {
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			return errors.New("the statement returned no row")
		}
		values, err := rows.Values()
		rows.Close()
		if err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(values) == 0 {
			return errors.New("the statement returned no column")
		}
		scalar := integerScalar(values[0])
		if statement.Result == core.ResultKey {
			result.Key = scalar
			return nil
		}
		count, ok := scalar.(int64)
		if !ok {
			return fmt.Errorf("the statement's reported affected-row count is not an integer: %T", values[0])
		}
		result.Affected = count
		return nil

	default: // core.ResultNone, core.ResultDriverCount: the command tag is the statement's own
		tag, err := pending.Exec()
		if err != nil {
			return err
		}
		result.Affected = tag.RowsAffected()
		return nil
	}
}

// integerScalar widens the integer widths pgx scans (int2 → int16, int4 → int32) to int64, the shape database/sql hands the
// session on every other path, so a generated key converts the same way whichever realization read it.
func integerScalar(value any) any {
	switch v := value.(type) {
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int:
		return int64(v)
	}
	return value
}

// sendAlone runs one statement the pipeline cannot carry, on the session's executor — the immediate path, so the
// simple protocol that takes a parameterless multi-statement command applies.
func sendAlone(ctx context.Context, exec core.SQLExecutor, statement core.MessageStatement) (core.MessageResult, error) {
	result, err := exec.ExecContext(ctx, statement.SQL, statement.Args...)
	if err != nil {
		return core.MessageResult{}, err
	}
	affected, err := result.RowsAffected()
	return core.MessageResult{Affected: affected}, err
}

// hasMultipleStatements reports whether sql holds more than one statement: a `;` outside string literals, quoted
// identifiers, dollar-quoted strings, and comments, followed by anything but whitespace and comments. The scan
// follows PostgreSQL's lexing for the cases that matter here (standard strings, nested block comments, `$tag$`
// quotes); an `E'…'` string with a backslash-escaped quote may end early in it, which can only make a single
// statement look like several — and a registry command that is sent alone is merely sent alone.
func hasMultipleStatements(sqlText string) bool {
	semicolon := false // anything real after a `;` makes it a second statement
	for i := 0; i < len(sqlText); {
		switch c := sqlText[i]; {
		case c == ';':
			semicolon = true
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '-' && i+1 < len(sqlText) && sqlText[i+1] == '-':
			for i < len(sqlText) && sqlText[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(sqlText) && sqlText[i+1] == '*':
			i = skipBlockComment(sqlText, i)
		case c == '\'' || c == '"':
			if semicolon {
				return true
			}
			i = skipQuoted(sqlText, i, c)
		case c == '$':
			if semicolon {
				return true
			}
			i = skipDollarQuoted(sqlText, i)
		default:
			if semicolon {
				return true
			}
			i++
		}
	}
	return false
}

// skipQuoted returns the index after the string literal or quoted identifier opening at i; a doubled quote is an escaped one.
func skipQuoted(sqlText string, i int, quote byte) int {
	for i++; i < len(sqlText); i++ {
		if sqlText[i] != quote {
			continue
		}
		if i+1 < len(sqlText) && sqlText[i+1] == quote {
			i++
			continue
		}
		return i + 1
	}
	return len(sqlText)
}

// skipBlockComment returns the index after the block comment opening at i; PostgreSQL's nest.
func skipBlockComment(sqlText string, i int) int {
	depth := 0
	for i < len(sqlText) {
		switch {
		case strings.HasPrefix(sqlText[i:], "/*"):
			depth++
			i += 2
		case strings.HasPrefix(sqlText[i:], "*/"):
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return len(sqlText)
}

// skipDollarQuoted returns the index after the `$tag$ … $tag$` string opening at i, or after the `$` when it opens none
// (a lone `$`, or a `$1`).
func skipDollarQuoted(sqlText string, i int) int {
	end := i + 1
	for end < len(sqlText) && (sqlText[end] == '_' || sqlText[end] >= 'a' && sqlText[end] <= 'z' || sqlText[end] >= 'A' && sqlText[end] <= 'Z' ||
		end > i+1 && sqlText[end] >= '0' && sqlText[end] <= '9') {
		end++
	}
	if end >= len(sqlText) || sqlText[end] != '$' {
		return i + 1
	}
	tag := sqlText[i : end+1]
	if close := strings.Index(sqlText[end+1:], tag); close >= 0 {
		return end + 1 + close + len(tag)
	}
	return len(sqlText)
}
