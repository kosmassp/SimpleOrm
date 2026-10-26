package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// Statement batching on SQL Server (Level 4 item 3, ADR-0044, spec/dialects.md "Statement batching"): go-mssqldb has
// no batch API, so a planned message goes out as one multi-statement command text — the statements joined in order by `;` and a
// line break (so a statement ending in a line comment cannot swallow the next), sent as one sp_executesql — with each statement's placeholders renamed apart (`@name` in the i-th statement,
// from 0, becomes `@p<i>_name`, literals and comments untouched) and its results read with NextResultSet. The session
// reaches it through the optional core.StatementBatcher beside the seam.
//
// SQL Server caps one sp_executesql at 2100 parameters, two of them its own — hence Dialect.MaxParametersPerMessage
// 2098, which the session's plan applies on every dialect so the message is formed the same everywhere.

var _ core.StatementBatcher = (*Dialect)(nil)

// SendMessage sends the statements as multi-statement texts and reads their results in order. The statements that
// return a result — an insert whose key the database generates (`select cast(scope_identity() as bigint)`) and every
// generated update or delete (`select @@rowcount`, ReportsAffectedRowsBySelect) — return exactly one 1×1 result set
// each, in statement order; go-mssqldb skips the result-less ones (an insert's own row count) itself. A statement
// whose own count the driver cannot tell apart from its neighbours' — a registry command, counted by the driver —
// is sent on its own through the session's executor, between the texts of the statements around it: more round trips
// than the plan's message, never fewer, and in order.
//
// A statement-terminating error (a constraint) does not end a SQL Server batch: the statements after it still run, and
// the driver reports the error. The session fails the call, which is atomic through its transaction (spec/session.md
// "Batching", Failure).
func (d *Dialect) SendMessage(ctx context.Context, _ *sql.Conn, exec core.SQLExecutor, statements []core.MessageStatement) ([]core.MessageResult, error) {
	results := make([]core.MessageResult, len(statements))
	for start := 0; start < len(statements); {
		end := start
		for end < len(statements) && statements[end].Result != core.ResultDriverCount {
			end++
		}
		if end > start {
			if err := sendText(ctx, exec, statements[start:end], results[start:end]); err != nil {
				return nil, err
			}
			start = end
			continue
		}
		core.StatementLogFrom(ctx).Statement(statements[start].Name, statements[start].SQL, statements[start].Args)
		result, err := exec.ExecContext(ctx, statements[start].SQL, statements[start].Args...)
		if err != nil {
			return nil, err
		}
		if results[start].Affected, err = result.RowsAffected(); err != nil {
			return nil, err
		}
		start++
	}
	return results, nil
}

// sendText sends one multi-statement text and reads its scalar results.
func sendText(ctx context.Context, exec core.SQLExecutor, statements []core.MessageStatement, results []core.MessageResult) error {
	text, args, err := renderMessage(ctx, statements)
	if err != nil {
		return err
	}
	if !readsAnyResult(statements) {
		_, err := exec.ExecContext(ctx, text, args...)
		return err
	}
	rows, err := exec.QueryContext(ctx, text, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return readScalars(rows, statements, results)
}

func readsAnyResult(statements []core.MessageStatement) bool {
	for _, statement := range statements {
		if returnsScalar(statement) {
			return true
		}
	}
	return false
}

func returnsScalar(statement core.MessageStatement) bool {
	return statement.Result == core.ResultKey || statement.Result == core.ResultReportedCount
}

// joinMessage is the message's command text and arguments: the statements renamed apart and joined in order by `;` and a line break.
func joinMessage(statements []core.MessageStatement) (string, []any, error) {
	parts, partArgs, err := renameMessage(statements)
	if err != nil {
		return "", nil, err
	}
	var args []any
	for _, renamed := range partArgs {
		args = append(args, renamed...)
	}
	return strings.Join(parts, ";\n"), args, nil
}

// renderMessage is joinMessage plus the statement log (ADR-0045): once every statement has renamed cleanly, one line per
// statement is written — each the renamed text and renamed parameters this realization sends — before the message goes out.
func renderMessage(ctx context.Context, statements []core.MessageStatement) (string, []any, error) {
	parts, partArgs, err := renameMessage(statements)
	if err != nil {
		return "", nil, err
	}
	var args []any
	for _, renamed := range partArgs {
		args = append(args, renamed...)
	}
	if log := core.StatementLogFrom(ctx); log != nil {
		for i, statement := range statements {
			log.Statement(statement.Name, parts[i], partArgs[i])
		}
	}
	return strings.Join(parts, ";\n"), args, nil
}

// renameMessage renames each statement of a message apart: its text and its arguments, statement by statement.
func renameMessage(statements []core.MessageStatement) ([]string, [][]any, error) {
	parts := make([]string, len(statements))
	partArgs := make([][]any, len(statements))
	for i, statement := range statements {
		text, renamed, err := renameApart(i, statement.SQL, statement.Args)
		if err != nil {
			return nil, nil, err
		}
		parts[i], partArgs[i] = text, renamed
	}
	return parts, partArgs, nil
}

// renameApart gives the i-th statement of a message its own parameter names: every real `@name` occurrence in the SQL
// (never inside a literal or comment, and never an `@@` system function — core.FindPlaceholders, the binder's own scanner) becomes `@p<i>_<name>` and its
// argument is bound under the new name. Names are lower-cased on both sides, so SQL Server's case rules for variables
// cannot split a name in two. A positional argument is refused: the session binds every value with sql.Named.
func renameApart(index int, sqlText string, args []any) (string, []any, error) {
	prefix := "p" + strconv.Itoa(index) + "_"

	type occurrence struct {
		start, length int
		name          string
	}
	var occurrences []occurrence
	seen := map[string]bool{}
	for _, placeholder := range core.FindPlaceholders(sqlText) {
		name := strings.ToLower(placeholder)
		if seen[name] {
			continue // PlaceholderOccurrences matches every spelling of a name at once
		}
		seen[name] = true
		for _, span := range core.PlaceholderOccurrences(sqlText, name) {
			occurrences = append(occurrences, occurrence{span.Index, span.Length, name})
		}
	}
	sort.Slice(occurrences, func(i, j int) bool { return occurrences[i].start < occurrences[j].start })

	var text strings.Builder
	last := 0
	for _, o := range occurrences {
		text.WriteString(sqlText[last:o.start])
		text.WriteString("@" + prefix + o.name)
		last = o.start + o.length
	}
	text.WriteString(sqlText[last:])

	renamed := make([]any, len(args))
	for i, arg := range args {
		named, ok := arg.(sql.NamedArg)
		if !ok {
			return "", nil, fmt.Errorf("sqlserver: positional argument %d beside @name placeholders; bind every value with sql.Named", i+1)
		}
		renamed[i] = sql.Named(prefix+strings.ToLower(named.Name), named.Value)
	}
	return text.String(), renamed, nil
}

// resultSets is the part of *sql.Rows the reader uses — the reading order is the logic worth testing without a server.
type resultSets interface {
	Next() bool
	NextResultSet() bool
	Scan(dest ...any) error
	Err() error
}

// readScalars reads the 1×1 result of each statement that has one, in statement order, then drains what is left of
// the response so an error raised by a statement after the last result surfaces here.
func readScalars(rows resultSets, statements []core.MessageStatement, results []core.MessageResult) error {
	first := true
	for i, statement := range statements {
		if !returnsScalar(statement) {
			continue
		}
		if !first && !rows.NextResultSet() {
			if err := rows.Err(); err != nil {
				return err
			}
			return fmt.Errorf("sqlserver: the message returned no result for statement %d of %d", i+1, len(statements))
		}
		first = false
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			return fmt.Errorf("sqlserver: statement %d of %d returned no row", i+1, len(statements))
		}
		var value any
		if err := rows.Scan(&value); err != nil {
			return err
		}
		// Finish the set before advancing: go-mssqldb learns the next set's columns only when Next runs into them, so
		// NextResultSet right after the one row would find none.
		for rows.Next() {
		}
		scalar, err := integerOf(value)
		if err != nil {
			return fmt.Errorf("sqlserver: statement %d of %d: %w", i+1, len(statements), err)
		}
		if statement.Result == core.ResultKey {
			results[i].Key = scalar
		} else {
			results[i].Affected = scalar
		}
	}
	for rows.NextResultSet() {
		for rows.Next() {
		}
	}
	return rows.Err()
}

// integerOf reads a scalar as an integer whatever the driver's shape (go-mssqldb returns @@rowcount as a 32-bit
// integer, the cast scope_identity() as a 64-bit one; spec/dialects.md Clarification 24). NULL — a scope_identity()
// with no insert, a missing count — is a dialect bug, not a zero.
func integerOf(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int:
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case []byte:
		return strconv.ParseInt(string(v), 10, 64)
	case string:
		return strconv.ParseInt(v, 10, 64)
	case nil:
		return 0, fmt.Errorf("the scalar result is NULL")
	}
	return 0, fmt.Errorf("the scalar result is not an integer: %T", value)
}
