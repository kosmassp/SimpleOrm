package migrations

import "strings"

// CreateTableSQL renders a table snapshot straight to DDL (ADR-0018): a
// (future) derived rollback restores columns from it, and a (future) shadow
// replayer's trusted `--from` baseline rebuilds whole tables from it
// (ADR-0017). Byte-identical to the C# reference's SnapshotDdl.CreateTableSql.
func CreateTableSQL(schema *TableSchema) string {
	var b strings.Builder
	b.WriteString("create table if not exists ")
	b.WriteString(schema.Name)
	b.WriteString(" (")
	first := true
	for _, column := range schema.Columns {
		if first {
			b.WriteString("\n    ")
		} else {
			b.WriteString(",\n    ")
		}
		first = false
		b.WriteString(column.Name)
		b.WriteByte(' ')
		if column.Key && column.Generated {
			b.WriteString("INTEGER PRIMARY KEY") // the rowid alias spelling
			continue
		}
		b.WriteString(column.StorageType)
		if !column.Nullable {
			b.WriteString(" NOT NULL")
		}
	}

	var plainKeys []string
	hasGeneratedKey := false
	for _, column := range schema.Columns {
		if column.Key && column.Generated {
			hasGeneratedKey = true
		}
		if column.Key && !column.Generated {
			plainKeys = append(plainKeys, column.Name)
		}
	}
	if len(plainKeys) > 0 && !hasGeneratedKey {
		b.WriteString(",\n    primary key (")
		b.WriteString(strings.Join(plainKeys, ", "))
		b.WriteByte(')')
	}

	b.WriteString("\n) STRICT")
	return b.String()
}

// CreateIndexSQL renders one index's create statement.
func CreateIndexSQL(objectName string, index TableIndex) string {
	unique := ""
	if index.Unique {
		unique = "unique "
	}
	columns := make([]string, len(index.Columns))
	for i, part := range index.Columns {
		columns[i] = part.ColumnName
		if part.Descending {
			columns[i] += " desc"
		}
	}
	return "create " + unique + "index if not exists " + index.Name + " on " + objectName +
		" (" + strings.Join(columns, ", ") + ")"
}

// CreateIndexSQLs renders every index of schema.
func CreateIndexSQLs(schema *TableSchema) []string {
	out := make([]string, len(schema.Indexes))
	for i, index := range schema.Indexes {
		out[i] = CreateIndexSQL(schema.Name, index)
	}
	return out
}
