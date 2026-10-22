// Package sample holds the fixture entities of the Go port — the models of
// dotnet/samples/SimpleOrm.Sample/Models, one to one (CODING-STANDARD §7) —
// and their migrations tree. Their metadata exports are pinned by
// conformance/entities/*.json; the tests and conformance runners build the
// fixture database from them.
package sample

import "time"

// BaseModel carries the audit columns shared by every sample table (mirrors
// dotnet/samples BaseModel): embedded fields map, ordered after the embedding
// struct's own. The explicit column names keep the UTC-signaling field names
// while matching the actual columns (convention alone would give
// created_at_utc).
type BaseModel struct {
	// CreatedAtUtc is ISO-8601 UTC TEXT in the database (§7.9): a time.Time in UTC. The created_at hook (Level 3 M3,
	// spec/hooks.md) fills it on insert when unset.
	CreatedAtUtc time.Time `orm:"column=created_at,created_at"`
	// UpdatedAtUtc is null until the row is first updated; the updated_at hook stamps it on every write that changes the row.
	UpdatedAtUtc *time.Time `orm:"column=updated_at,updated_at"`
}
