package core

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// The statement log (Level 4 item 4, ADR-0045, spec/session.md "Statement log"): one line of text per statement the
// library sends, written as the statement is sent — after it is bound, before it executes — so the application sees
// what the database receives. The library brings no logging dependency: the application hands over a function of one
// string (or the built-in stdout sink) and forwards the line to its own logger.
//
// The session holds one *StatementLog (nil when the option is unset, which is the Level 0–3 behaviour exactly: nothing is
// written and nothing is formatted). The tooling (the migration runner, the shadow, force-sync, SchemaGuard's
// introspection) and the dialects' one-round-trip realizations (StatementBatcher.SendMessage) cannot take a session
// parameter without widening their public surface, so they reach the same log through the context: WithStatementLog
// attaches it, WithStatementName says what the statements that follow are called (CODING-STANDARD §10).

// StatementLog writes statement lines to a sink.
type StatementLog struct {
	write  func(string)
	values bool
}

// NewStatementLog is the log over write; values says whether a line carries parameter values beside their names. It is
// nil when write is nil, and every method is nil-safe, so "unset" costs one pointer test.
func NewStatementLog(write func(string), values bool) *StatementLog {
	if write == nil {
		return nil
	}
	return &StatementLog{write: write, values: values}
}

// Statement writes the line of one statement: name, the SQL exactly as sent, and its bound parameters (args holds the
// session's sql.NamedArg values, in bind order). The line is formatted only when a sink is set.
func (l *StatementLog) Statement(name, sqlText string, args []any) {
	if l == nil {
		return
	}
	l.write(FormatStatementLine(name, sqlText, args, l.values))
}

// FormatStatementLine is the line itself: `<name>: <sql>`, then — when the statement has parameters — a space and
// their names in bind order in square brackets (`[@k0]`, `[@Name, @Id]`), or with values on `[@Name = 'Ada', @Id = 7]`.
func FormatStatementLine(name, sqlText string, args []any, values bool) string {
	var line strings.Builder
	line.WriteString(name)
	line.WriteString(": ")
	line.WriteString(sqlText)
	if len(args) == 0 {
		return line.String()
	}
	line.WriteString(" [")
	for i, arg := range args {
		if i > 0 {
			line.WriteString(", ")
		}
		parameterName, value := "@p"+strconv.Itoa(i+1), arg
		if named, ok := arg.(sql.NamedArg); ok {
			parameterName, value = "@"+named.Name, named.Value
		}
		line.WriteString(parameterName)
		if values {
			line.WriteString(" = ")
			line.WriteString(FormatLogValue(value))
		}
	}
	line.WriteString("]")
	return line.String()
}

// FormatLogValue is a parameter value in the log's fixed, culture-neutral form: a string single-quoted with `'` doubled,
// null as `null`, a bool as `true`/`false`, bytes as `0x` + hex of the first 16 followed by the length, an instant as
// quoted ISO-8601 UTC text, a slice (an array parameter) in square brackets, every number in its invariant form.
func FormatLogValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	case bool:
		return strconv.FormatBool(v)
	case []byte:
		if v == nil {
			return "null"
		}
		shown := v
		if len(shown) > 16 {
			shown = shown[:16]
		}
		more := ""
		if len(v) > len(shown) {
			more = "…"
		}
		return "0x" + hex.EncodeToString(shown) + more + " (" + strconv.Itoa(len(v)) + " bytes)"
	case time.Time:
		return "'" + FormatUTC(v) + "'"
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(v)
	case driver.Valuer:
		if reflect.ValueOf(v).Kind() == reflect.Pointer && reflect.ValueOf(v).IsNil() {
			return "null"
		}
		inner, err := v.Value()
		if err != nil {
			return fmt.Sprint(v)
		}
		return FormatLogValue(inner)
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer:
		if reflected.IsNil() {
			return "null"
		}
		return FormatLogValue(reflected.Elem().Interface())
	case reflect.Slice, reflect.Array:
		parts := make([]string, reflected.Len())
		for i := range parts {
			parts[i] = FormatLogValue(reflected.Index(i).Interface())
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case reflect.String:
		return FormatLogValue(reflected.String())
	}
	return fmt.Sprint(value)
}

type statementLogKey struct{}
type statementNameKey struct{}

// WithStatementLog attaches the log to ctx; a nil log returns ctx unchanged.
func WithStatementLog(ctx context.Context, log *StatementLog) context.Context {
	if log == nil {
		return ctx
	}
	return context.WithValue(ctx, statementLogKey{}, log)
}

// StatementLogFrom is the log attached to ctx, or nil.
func StatementLogFrom(ctx context.Context) *StatementLog {
	log, _ := ctx.Value(statementLogKey{}).(*StatementLog)
	return log
}

// WithStatementName names the statements issued under ctx (`migrate`, `shadow`, `sync`, `validate <source>`),
// replacing any name already set. Without a log on ctx it returns ctx unchanged.
func WithStatementName(ctx context.Context, name string) context.Context {
	if StatementLogFrom(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, statementNameKey{}, name)
}

// WithDefaultStatementName is WithStatementName for an entry point that yields to a caller's own name: the migration
// runner replaying under `shadow` stays `shadow`.
func WithDefaultStatementName(ctx context.Context, name string) context.Context {
	if StatementLogFrom(ctx) == nil {
		return ctx
	}
	if existing, _ := ctx.Value(statementNameKey{}).(string); existing != "" {
		return ctx
	}
	return context.WithValue(ctx, statementNameKey{}, name)
}

// LogStatement writes one line under the name and log ctx carries — the one call every tooling site makes as it sends a
// statement. It does nothing, and formats nothing, when ctx carries no log.
func LogStatement(ctx context.Context, sqlText string, args ...any) {
	log := StatementLogFrom(ctx)
	if log == nil {
		return
	}
	name, _ := ctx.Value(statementNameKey{}).(string)
	if name == "" {
		name = "statement"
	}
	log.Statement(name, sqlText, args)
}
