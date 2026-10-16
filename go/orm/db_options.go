package orm

// Options configures a [Db] session (§7.17, mirrors dotnet/src/SimpleOrm/DbOptions.cs):
// the dialect, the metadata configuration, and the type-handler registry —
// fixed for the session's lifetime. The session owns its own metadata cache
// built from these options, so an entity's map loads once per session.
type Options struct {
	// Dialect provides the connection and every generated/rendered SQL surface
	// (§7.25). Required: Open refuses a nil Dialect.
	Dialect Dialect
	// Mapping configures metadata loading: the naming convention and explicit
	// maps (spec/metadata-model.md). nil means the defaults (snake_case, no
	// explicit maps).
	Mapping *MappingOptions
	// TypeHandlers is the custom and JSON handler registry (§7.9/§7.10) — the
	// extension point beyond the fixed conversion table. nil means none
	// registered.
	TypeHandlers *TypeHandlerRegistry
	// Tracking chooses the session mode (Level 3 M1, ADR-0034 add.1): the
	// zero value TrackingUntracked is the Level 0–2 session exactly; opt in
	// with TrackingTracked for the identity map and change snapshots.
	Tracking TrackingMode
}

// TrackingMode is the session's tracking mode, fixed at open (ADR-0034 add.1
// ruling 1: tracking is configured, never implicit). The default is untracked
// so every Level 0–2 behaviour and benchmark is untouched.
type TrackingMode int

const (
	// TrackingUntracked is the default: every read materializes a fresh
	// instance, Update writes the full row, nothing is remembered.
	TrackingUntracked TrackingMode = iota
	// TrackingTracked keeps one instance per (entity type, key) and a snapshot
	// of its mapped values (spec/session.md "Tracking").
	TrackingTracked
)
