// Package sqlserver is the SQL Server dialect of the Go port (ADR-0024,
// spec/dialects.md) — the C# SimpleOrm.SqlServer assembly. It is backed by
// github.com/microsoft/go-mssqldb, the pure-Go TDS driver: the Go analog of
// Microsoft.Data.SqlClient. The module is the package's only dependency; this
// file is the one place it is imported (driver.go also reaches its connection-
// string parser, which lives in the same module).
package sqlserver

import (
	"database/sql"
	"errors"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
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

// transientErrorNumbers is the SQL Server number set of spec/dialects.md "Transient open failures" (ADR-0047): the
// configurable-retry defaults without the lock numbers, the connection-level numbers, and -2, the connect timeout
// elapsing. Login failed (18456) is not in it.
var transientErrorNumbers = map[int32]bool{
	-2: true, 20: true, 64: true, 121: true, 233: true, 997: true, 4060: true, 4221: true,
	10053: true, 10054: true, 10060: true, 10928: true, 10929: true, 11001: true, 17197: true, 18401: true,
	40143: true, 40197: true, 40501: true, 40540: true, 40613: true, 42108: true, 42109: true, 49918: true,
	49919: true, 49920: true,
}

// IsTransient answers whether a failed open may succeed on another attempt (ADR-0047, spec/dialects.md "Transient
// open failures"): an mssql.Error anywhere in the cause chain whose number — or the number of any of the error records
// it carries (Error.All) — is in the set, else a socket, I/O, or timeout failure of the Go runtime
// (core.IsTransientTransport: go-mssqldb reports an unreachable server as the dial's *net.OpError). A pure function of
// the failure; never asked about a statement.
func (*Dialect) IsTransient(err error) bool {
	if err == nil {
		return false
	}
	// An engine answer settles it: the server spoke, and a number outside the set (18456 login failed) is what it will
	// say again, whatever transport failure the chain also carries (CODING-STANDARD §10).
	var value mssql.Error
	if errors.As(err, &value) {
		return carriesTransientNumber(value)
	}
	var pointer *mssql.Error
	if errors.As(err, &pointer) && pointer != nil {
		return carriesTransientNumber(*pointer)
	}
	return core.IsTransientTransport(err)
}

func carriesTransientNumber(failure mssql.Error) bool {
	if transientErrorNumbers[failure.Number] {
		return true
	}
	for _, record := range failure.All {
		if transientErrorNumbers[record.Number] {
			return true
		}
	}
	return false
}
