package core

// TemporalMarkers is the optional dialect capability behind the §7.9 read rule
// on databases with real date types (spec/dialects.md "Temporals"): a Go
// time.Time always carries a location, so a driver-native temporal has no
// "unspecified" state to detect by value — the marker lives in the column's
// declared type instead. A dialect implements this beside Dialect; the result
// mapper type-asserts it and, where a column read into a temporal property is
// markerless, refuses with VAL-020 (a registered type handler for the property
// remains the legacy-column escape, handlers winning over the fixed table).
// SQLite does not implement it: its temporals are TEXT and carry their own
// marker. Not a Dialect member, so the seam keeps exactly the reference's
// member set (§7.25, CODING-STANDARD §10).
type TemporalMarkers interface {
	// IsMarkerlessTemporal reports whether a column of this driver-reported
	// type name (sql.ColumnType.DatabaseTypeName, compared case-insensitively)
	// stores instants without a UTC/offset marker: SQL Server datetime,
	// datetime2, smalldatetime; PostgreSQL timestamp (without time zone).
	// Marker-carrying storage (datetimeoffset, timestamptz) and date/time
	// columns, which carry no kind by design, return false.
	IsMarkerlessTemporal(databaseTypeName string) bool
}
