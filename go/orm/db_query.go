package orm

import (
	"context"
	"database/sql"
	"iter"
	"reflect"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/params"
)

// --- the registry surface (§6, spec/session.md) -----------------------------

// Query runs a registered query and materializes every row (the C#
// QueryAsync<TArgs, TResult>).
func Query[TArgs, TResult any](ctx context.Context, db *Db, query QueryEntry[TArgs, TResult], args TArgs) ([]TResult, error) {
	sqlText, err := query.Source().SQL()
	if err != nil {
		return nil, err
	}
	rows, err := bindAndQuery(ctx, db, sqlText, args, query.Source().Description())
	if err != nil {
		return nil, err
	}
	return materializeRows[TResult](ctx, db, rows, query.Source().Description())
}

// QuerySingle expects exactly one row: zero is QRY-001, more than one QRY-002.
func QuerySingle[TArgs, TResult any](ctx context.Context, db *Db, query QueryEntry[TArgs, TResult], args TArgs) (TResult, error) {
	rows, err := Query(ctx, db, query, args)
	var zero TResult
	if err != nil {
		return zero, err
	}
	switch len(rows) {
	case 1:
		return rows[0], nil
	case 0:
		return zero, core.NewError("QRY-001", query.Source().Description(), "expected exactly one row, found none")
	default:
		return zero, core.Errorf("QRY-002", query.Source().Description(), "expected exactly one row, found %d", len(rows))
	}
}

// QuerySingleOrDefault expects at most one row: zero returns nil, more than one is QRY-002.
func QuerySingleOrDefault[TArgs, TResult any](ctx context.Context, db *Db, query QueryEntry[TArgs, TResult], args TArgs) (*TResult, error) {
	rows, err := Query(ctx, db, query, args)
	if err != nil {
		return nil, err
	}
	switch len(rows) {
	case 0:
		return nil, nil
	case 1:
		return &rows[0], nil
	default:
		return nil, core.Errorf("QRY-002", query.Source().Description(), "expected at most one row, found %d", len(rows))
	}
}

// Stream delivers rows one at a time as the caller ranges over the sequence;
// nothing is buffered. The plan is built (MAP-001/002 checked) before the
// first row is yielded, exactly like Query.
func Stream[TArgs, TResult any](ctx context.Context, db *Db, query QueryEntry[TArgs, TResult], args TArgs) iter.Seq2[TResult, error] {
	return func(yield func(TResult, error) bool) {
		var zero TResult
		sqlText, err := query.Source().SQL()
		if err != nil {
			yield(zero, err)
			return
		}
		rows, err := bindAndQuery(ctx, db, sqlText, args, query.Source().Description())
		if err != nil {
			yield(zero, err)
			return
		}
		streamRows(ctx, db, rows, query.Source().Description(), yield)
	}
}

// Execute runs a non-query command and returns the affected-row count.
func Execute[TArgs any](ctx context.Context, db *Db, command CommandEntry[TArgs], args TArgs) (int64, error) {
	sqlText, err := command.Source().SQL()
	if err != nil {
		return 0, err
	}
	bound, err := params.Bind(sqlText, args, db.converter, db.options.Dialect, command.Source().Description())
	if err != nil {
		return 0, err
	}
	result, err := db.exec().ExecContext(ctx, bound.SQL, bound.Args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// --- statement-backed entities (ADR-0010): the type IS the query ------------

// openStatement runs a [Statement]-backed entity's own SQL: QRY-004 when
// resultType is not statement-backed, then PRM-012 when an args field's type
// differs from the declared parameter type, before binding.
func openStatement(ctx context.Context, db *Db, resultType reflect.Type, args any) (*sql.Rows, string, error) {
	name := resultType.Name() + " [Statement]"
	m, err := db.maps.Load(resultType)
	if err != nil {
		return nil, name, err
	}
	if m.Kind != core.RelationStatement {
		return nil, name, core.Errorf("QRY-004", resultType.Name(),
			"is %s-backed, not statement-backed; use the registry or generated CRUD for it", m.Kind)
	}
	if err := checkStatementParameterTypes(m, args, name); err != nil {
		return nil, name, err
	}
	rows, err := bindAndQuery(ctx, db, m.DefiningSQL, args, name)
	return rows, name, err
}

// checkStatementParameterTypes mirrors Db.cs's args-vs-declaration check: the
// loader already proved declared parameters == SQL placeholders (PRM-010/011);
// here the args value must match the declaration in type as well as name
// (name matching case-insensitive, pointer wrappers transparent).
func checkStatementParameterTypes(m *core.EntityMap, args any, queryName string) error {
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil // params.Bind reports the real problem (PRM-001/002)
	}
	t := v.Type()
	for _, parameter := range m.StatementParameters {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" || !strings.EqualFold(field.Name, parameter.Name) {
				continue
			}
			declared := stripPointer(parameter.Type)
			actual := stripPointer(field.Type)
			if declared != actual {
				return core.Errorf("PRM-012", queryName+"."+parameter.Name,
					"declared as %s, args supply %s", declared, actual)
			}
			break
		}
	}
	return nil
}

// QueryStatement runs a statement-backed entity's own SQL; args bind against its declared parameters.
func QueryStatement[TResult any](ctx context.Context, db *Db, args any) ([]TResult, error) {
	rows, name, err := openStatement(ctx, db, reflect.TypeFor[TResult](), args)
	if err != nil {
		return nil, err
	}
	return materializeRows[TResult](ctx, db, rows, name)
}

func statementName[TResult any]() string { return reflect.TypeFor[TResult]().Name() + " [Statement]" }

// QueryStatementSingle mirrors QuerySingle for a statement-backed entity.
func QueryStatementSingle[TResult any](ctx context.Context, db *Db, args any) (TResult, error) {
	rows, err := QueryStatement[TResult](ctx, db, args)
	var zero TResult
	if err != nil {
		return zero, err
	}
	name := statementName[TResult]()
	switch len(rows) {
	case 1:
		return rows[0], nil
	case 0:
		return zero, core.NewError("QRY-001", name, "expected exactly one row, found none")
	default:
		return zero, core.Errorf("QRY-002", name, "expected exactly one row, found %d", len(rows))
	}
}

// QueryStatementSingleOrDefault mirrors QuerySingleOrDefault for a statement-backed entity.
func QueryStatementSingleOrDefault[TResult any](ctx context.Context, db *Db, args any) (*TResult, error) {
	rows, err := QueryStatement[TResult](ctx, db, args)
	if err != nil {
		return nil, err
	}
	name := statementName[TResult]()
	switch len(rows) {
	case 0:
		return nil, nil
	case 1:
		return &rows[0], nil
	default:
		return nil, core.Errorf("QRY-002", name, "expected at most one row, found %d", len(rows))
	}
}

// StreamStatement mirrors Stream for a statement-backed entity.
func StreamStatement[TResult any](ctx context.Context, db *Db, args any) iter.Seq2[TResult, error] {
	return func(yield func(TResult, error) bool) {
		var zero TResult
		rows, name, err := openStatement(ctx, db, reflect.TypeFor[TResult](), args)
		if err != nil {
			yield(zero, err)
			return
		}
		streamRows(ctx, db, rows, name, yield)
	}
}
