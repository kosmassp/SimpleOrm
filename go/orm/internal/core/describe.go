package core

import (
	"context"
	"database/sql"
)

// ColumnDescription is one result column of a prepared statement, as
// SchemaGuard needs it (§7.19): the result name, the declared type, and the
// origin table/column when the column traces to a base table — empty for an
// expression column, whose nullability is unknowable (the stricter direction).
type ColumnDescription struct {
	Name         string
	DeclaredType string
	Table        string
	OriginColumn string
}

// StatementDescriber describes a statement's result columns **without
// executing it** — the C# reference reads them through ADO.NET's
// CommandBehavior.SchemaOnly + GetColumnSchema, which database/sql has no
// provider-neutral analog of (CODING-STANDARD §10). A dialect implements this
// optional interface beside Dialect; SchemaGuard type-asserts it. It is not a
// Dialect member so the seam keeps exactly the reference's member set (§7.25).
type StatementDescriber interface {
	DescribeStatement(ctx context.Context, conn *sql.Conn, sql string) ([]ColumnDescription, error)
}
