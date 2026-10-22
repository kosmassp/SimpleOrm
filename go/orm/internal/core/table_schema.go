package core

// The snapshot shape of a table (spec/migrations.md "Snapshots", format v2/v3): provider-agnostic but in
// storage terms. It lives in core because the dialect seam renders DDL from it (ADR-0043:
// Dialect.CreateTableFromSchemaSQL, CreateIndexFromSchemaSQL), and the migrations package, which produces and
// reads it, aliases these types.

// TableColumn is one column of a TableSchema, in storage terms.
type TableColumn struct {
	Name        string
	StorageType string
	Nullable    bool
	Key         bool
	Generated   bool
}

// IndexPart is one column of a TableIndex, in index order.
type IndexPart struct {
	ColumnName string
	Descending bool
}

// TableIndex is one index of a TableSchema.
type TableIndex struct {
	Name    string
	Columns []IndexPart
	Unique  bool
}

// TableSchema is a table's shape in storage types (what CREATE TABLE emits and introspection reports back —
// the per-table schema snapshot, ADR-0013 add.3, ADR-0017). Both snapshot producers build this: the metadata
// exporter (`snapshot`) and the shadow replayer's introspection — which is what makes the two comparable.
// Dialect is the stamp of the document it was read from (format v3, ADR-0043): "" for an unstamped document,
// which is SQLite's; producers leave it empty.
type TableSchema struct {
	Name    string
	Dialect string
	Columns []TableColumn
	Indexes []TableIndex
}
