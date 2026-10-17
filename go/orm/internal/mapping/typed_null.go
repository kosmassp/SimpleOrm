package mapping

import (
	"database/sql"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// ToParameter is ToDatabase for a value about to be bound as a statement parameter: the same
// conversion, then a NULL carries the column's type from the metadata token (TypedNull) instead
// of an untyped nil (ADR-0040 add.1, spec/dialects.md "Typed nulls from the metadata token").
// Snapshots and comparisons keep calling ToDatabase — the typed null exists only on the way to
// the driver.
func (c *TypeConverter) ToParameter(value any, columnType core.ColumnType, context string) (any, error) {
	bound, err := c.ToDatabase(value, columnType, context)
	if err != nil || bound != nil {
		return bound, err
	}
	return TypedNull(columnType), nil
}

// TypedNull is the NULL a parameter of the token binds as: a value whose driver.Value is nil, so
// every driver writes SQL NULL, but whose Go type tells a driver that types its parameters from
// the value which SQL type the NULL has. go-mssqldb announces an untyped nil as nvarchar(1), and
// SQL Server refuses to convert an nvarchar into varbinary implicitly (error 257); a []byte(nil)
// it announces as varbinary. SQLite (modernc binds a nil []byte as NULL, the sql.Null types
// through driver.Valuer) and PostgreSQL (pgx encodes both as NULL; the server types the
// parameter) bind exactly what they bound before. Tokens with no SQL type of their own — date,
// time, decimal, guid, text enums — null as a string, which SQL Server converts implicitly to
// every one of those column types. A custom token (a handler type) stays an untyped nil.
func TypedNull(columnType core.ColumnType) any {
	switch columnType {
	case core.TypeBytes:
		return []byte(nil)
	case core.TypeInt16:
		return sql.NullInt16{}
	case core.TypeInt32:
		return sql.NullInt32{}
	case core.TypeInt64, core.TypeEnumInt:
		return sql.NullInt64{}
	case core.TypeDouble, core.TypeFloat:
		return sql.NullFloat64{}
	case core.TypeBool:
		return sql.NullBool{}
	case core.TypeDateTime, core.TypeDateTimeOffset:
		return sql.NullTime{}
	case core.TypeString, core.TypeDecimal, core.TypeGUID, core.TypeDate, core.TypeTime, core.TypeEnumText:
		return sql.NullString{}
	}
	return nil
}
