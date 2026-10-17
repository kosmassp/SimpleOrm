// Package sqlserver is the SQL Server dialect of the Go port (ADR-0024,
// spec/dialects.md) — the C# SimpleOrm.SqlServer assembly. It is backed by
// github.com/microsoft/go-mssqldb, the pure-Go TDS driver: the Go analog of
// Microsoft.Data.SqlClient. The module is the package's only dependency; this
// file is the one place it is imported (driver.go also reaches its connection-
// string parser, which lives in the same module).
package sqlserver

import (
	"database/sql"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
)

// openPool opens the driver pool for a go-mssqldb connection string — a
// `sqlserver://` URL, an ADO-style `server=…;database=…` string, or an
// `odbc:` string (spec/dialects.md "Connection strings and the CLI": the
// driver's native form). Two choices are made here, not in the string:
//
//   - the **sqlserver** connector (no query-text processing), so `@name`
//     placeholders and `?` inside user SQL reach the server untouched; and
//   - **guid conversion**, so a `uniqueidentifier` column is read in RFC 4122
//     byte order (SQL Server stores the first three groups little-endian; the
//     driver hands the raw bytes back unless told to convert), which is what
//     core.GUIDFromBytes expects.
//
// Nothing connects until first use (sql.OpenDB).
func openPool(connectionString string) (*sql.DB, error) {
	config, err := msdsn.Parse(connectionString)
	if err != nil {
		return nil, err
	}
	config.Encoding.GuidConversion = true
	return sql.OpenDB(mssql.NewConnectorConfig(config)), nil
}
