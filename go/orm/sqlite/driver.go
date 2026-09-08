// Package sqlite is the SQLite dialect of the Go port (ADR-0003, ADR-0027) —
// the C# SimpleOrm.Sqlite assembly. It is backed by modernc.org/sqlite, the
// pure-Go, cgo-free driver: the Go analog of Microsoft.Data.Sqlite and
// pdo_sqlite, and the port's only runtime dependency.
package sqlite

import (
	// The driver registers itself as "sqlite" with database/sql; this is the one import of it.
	_ "modernc.org/sqlite"
)

// DriverName is the database/sql driver name the dialect opens connections with.
const DriverName = "sqlite"
