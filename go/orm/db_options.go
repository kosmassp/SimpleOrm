package orm

import "time"

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
	// Clock is the instant the declared hooks write (Level 3 M3, spec/hooks.md): a function returning UTC —
	// an instant whose zone offset is not zero is VAL-020 when a hook reads it. nil means the wall clock
	// (time.Now().UTC()); tests and the conformance runners fix it at 2026-08-29T00:00:00Z.
	Clock func() time.Time
	// Batching chooses how a flush and an explicit batch send their statements (Level 4 item 3, ADR-0044,
	// spec/session.md "Batching"): the zero value BatchingOff is the Level 0–3 behaviour exactly, one message per
	// statement; BatchingAuto forms messages by the plan — as few as the driver allows — and runs them as one round
	// trip where the dialect can (db.BatchingInEffect), one statement at a time otherwise.
	Batching BatchingMode
	// MaxStatementsPerMessage is the most statements one message carries under BatchingAuto. The zero value is the
	// default, 100; a negative value is refused at Open (CODING-STANDARD §10: Go's zero value cannot tell "unset"
	// from "0", and the spec's "below 1 is the argument error" is read as "below 0 here").
	MaxStatementsPerMessage int
	// Log receives one line per statement the session sends -- after it is prepared and bound, before it executes, in the
	// order sent (Level 4 item 4, ADR-0045, spec/session.md "Statement log"). nil, the default, writes nothing and
	// formats nothing. ConsoleLog writes each line to standard output; forwarding to an application logger is a one-line
	// adapter. It runs on the calling goroutine; a panic in it is the application's.
	Log func(line string)
	// LogParameterValues adds the parameter values beside their names in each line. Off by default: values may be
	// personal data, and a log never receives them unless the application asks.
	LogParameterValues bool
	// CommandTimeout bounds how long every statement the session sends may run (Level 4, ADR-0047, spec/session.md
	// "Resilience"), in whole seconds rounded up, at least 1 (a positive sub-second value is 1 second). The zero value
	// is unset — Go's spelling of the spec's "unset", since a duration has no optional form: every statement keeps the
	// driver's own behaviour, untouched. database/sql has no per-command timeout, so each statement runs under
	// context.WithTimeout derived from the caller's context, and a result set's rows are read under the same deadline.
	// A negative value is refused at Open. A timeout that fires is the driver's error (errors.Is(err,
	// context.DeadlineExceeded), or the engine's cancellation error) while the caller's context is still live; nothing
	// is retried, rolled back, or closed because of it.
	CommandTimeout time.Duration
	// OpenRetries is how many more attempts Open makes after a failure the dialect calls transient (Dialect.IsTransient)
	// — 0, the default, is one attempt. Only the open is ever retried, never a statement. Negative is refused at Open.
	OpenRetries int
	// OpenRetryDelay is the fixed wait between two open attempts. The zero value is the default, one second
	// (DefaultOpenRetryDelay); NoOpenRetryDelay is the spelling of "no wait" (CODING-STANDARD §10). Negative is refused
	// at Open. The wait ends early, with the context's error and no further attempt, when the context does.
	OpenRetryDelay time.Duration
}

// DefaultOpenRetryDelay is the wait between two open attempts when Options leaves OpenRetryDelay unset (spec/session.md "Resilience").
const DefaultOpenRetryDelay = time.Second

// NoOpenRetryDelay is the OpenRetryDelay that means "no wait": the next attempt starts at once. Its value is one
// nanosecond, because the zero value already means the one-second default.
const NoOpenRetryDelay time.Duration = 1

// DefaultMaxStatementsPerMessage is the cap on statements per message when Options leaves it unset (spec/session.md "Batching").
const DefaultMaxStatementsPerMessage = 100

// BatchingMode is the session's batching option, fixed at open like the tracking mode (ADR-0044).
type BatchingMode int

const (
	// BatchingOff is the default: every statement is a message of its own.
	BatchingOff BatchingMode = iota
	// BatchingAuto forms the statements of one call (a Flush, the Send of an explicit batch) into messages by the plan.
	BatchingAuto
)

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
