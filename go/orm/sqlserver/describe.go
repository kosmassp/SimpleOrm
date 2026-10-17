package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// describeFirstResultSetSQL asks the server for the first result set's shape
// without running the statement; browse mode 1 adds the base table and column
// each result column traces to (the analog of GetColumnSchema's BaseTableName
// and BaseColumnName).
const describeFirstResultSetSQL = "exec sp_describe_first_result_set @tsql = @p1, @params = @p2, @browse_information_mode = 1"

// describeUndeclaredParametersSQL infers a type for each `@name` the statement
// uses; the first result's parameters need declarations, and a hand-written
// query has none.
const describeUndeclaredParametersSQL = "exec sp_describe_undeclared_parameters @tsql = @p1"

// DescribeStatement describes the statement's result columns without executing
// it (core.StatementDescriber, §7.19).
//
// The choice: `sp_describe_first_result_set` over `SET FMTONLY ON`.
// FMTONLY is deprecated, is session state that must be reset around a failure,
// returns names and types only (no source table or column — and SchemaGuard's
// nullability check needs the origin), and misreads batches with conditional
// branches or temp tables. The procedure analyzes the statement in the
// server's compiler, never executes it, reports `source_table`/`source_column`
// in browse mode, and raises the server's own error for SQL that does not
// parse (surfaced by the caller as VAL-001).
//
// The procedure needs the statement's parameters declared. They are inferred
// first with `sp_describe_undeclared_parameters` (its suggested type per
// parameter); a parameter the server cannot infer — or any failure of that
// inference — falls back to nvarchar(max), which never changes the *shape*
// of the result (only an expression over a parameter could change a column's
// type, and its nullability is unknowable anyway).
//
// A column with no source table is an expression column (empty Table and
// OriginColumn, the stricter direction); hidden browse-key columns are
// dropped. The table name is unqualified for the default `dbo` schema and
// `schema.table` otherwise, a form `object_id(@relation)` resolves.
func (*Dialect) DescribeStatement(ctx context.Context, conn *sql.Conn, statement string) ([]core.ColumnDescription, error) {
	declarations := declareParameters(ctx, conn, statement)
	var parameters any
	if declarations != "" {
		parameters = declarations
	}

	rows, err := conn.QueryContext(ctx, describeFirstResultSetSQL, statement, parameters)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type described struct {
		ordinal int64
		column  core.ColumnDescription
	}
	var found []described
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		cells := make([]any, len(names))
		targets := make([]any, len(names))
		for i := range cells {
			targets[i] = &cells[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		byName := make(map[string]any, len(names))
		for i, name := range names {
			byName[strings.ToLower(name)] = cells[i]
		}
		if asBool(byName["is_hidden"]) {
			continue
		}
		found = append(found, described{
			ordinal: asInt64(byName["column_ordinal"]),
			column: core.ColumnDescription{
				Name:         asString(byName["name"]),
				DeclaredType: asString(byName["system_type_name"]),
				Table:        qualifiedSource(asString(byName["source_schema"]), asString(byName["source_table"])),
				OriginColumn: asString(byName["source_column"]),
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(found, func(i, j int) bool { return found[i].ordinal < found[j].ordinal })
	columns := make([]core.ColumnDescription, len(found))
	for i, f := range found {
		columns[i] = f.column
	}
	return columns, nil
}

// declareParameters is the `@name type, …` list for statement's placeholders:
// the server's suggested type where it has one, nvarchar(max) otherwise; ""
// when the statement has no parameters. It never fails — inference is a
// convenience.
func declareParameters(ctx context.Context, conn *sql.Conn, statement string) string {
	placeholders := core.FindPlaceholders(statement)
	if len(placeholders) == 0 {
		return ""
	}
	suggested := suggestParameterTypes(ctx, conn, statement)
	declarations := make([]string, 0, len(placeholders))
	seen := map[string]bool{}
	for _, name := range placeholders {
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		declared := suggested[key]
		if declared == "" {
			declared = "nvarchar(max)"
		}
		declarations = append(declarations, "@"+name+" "+declared)
	}
	return strings.Join(declarations, ", ")
}

// suggestParameterTypes maps lowercase parameter names (without the `@`) to the
// type the server infers for them; empty on any failure.
func suggestParameterTypes(ctx context.Context, conn *sql.Conn, statement string) map[string]string {
	suggested := map[string]string{}
	rows, err := conn.QueryContext(ctx, describeUndeclaredParametersSQL, statement)
	if err != nil {
		return suggested
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return suggested
	}
	for rows.Next() {
		cells := make([]any, len(names))
		targets := make([]any, len(names))
		for i := range cells {
			targets[i] = &cells[i]
		}
		if rows.Scan(targets...) != nil {
			return map[string]string{}
		}
		var name, typeName string
		for i, column := range names {
			switch strings.ToLower(column) {
			case "name":
				name = asString(cells[i])
			case "suggested_system_type_name":
				typeName = asString(cells[i])
			}
		}
		if name != "" && typeName != "" {
			suggested[strings.ToLower(strings.TrimPrefix(name, "@"))] = typeName
		}
	}
	if rows.Err() != nil {
		return map[string]string{}
	}
	return suggested
}

// qualifiedSource is the relation name SchemaGuard hands back to
// ColumnsInfoSQL: "" when the column has no base table.
func qualifiedSource(schema, table string) string {
	if table == "" {
		return ""
	}
	if schema == "" || strings.EqualFold(schema, "dbo") {
		return table
	}
	return schema + "." + table
}

func asString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	}
	return fmt.Sprint(value)
}

func asBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	}
	return false
}

func asInt64(value any) int64 {
	if v, ok := value.(int64); ok {
		return v
	}
	return 0
}
