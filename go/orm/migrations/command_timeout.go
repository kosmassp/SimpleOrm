package migrations

import (
	"context"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The command timeout of the migration tooling (Level 4, ADR-0047, spec/session.md "Resilience"). The runner, the
// shadow, and force-sync open their own connections rather than a session's, so they take the timeout the way they take
// the statement log: WithCommandTimeout attaches it to the context, and every statement they send runs under a deadline
// that many seconds (rounded up, at least 1) from when it is sent — the version table, the recorded history, the steps,
// the guards' definition reads and probe, the rollbacks, the shadow's replay and introspection, the sync's introspection
// and statements. The CLI's --command-timeout sets it. Not covered — the dialect's own statements: the run lock, the
// shadow host's provisioning on the admin connection, and its trigger read-back; nor transaction control (begin,
// commit, rollback, the probe's savepoints).

// WithCommandTimeout returns ctx carrying timeout for the migration tooling; zero (unset) returns ctx unchanged, and the
// driver's own behaviour applies. A negative value is treated as unset; the CLI refuses one before anything opens.
func WithCommandTimeout(ctx context.Context, timeout time.Duration) context.Context {
	return core.WithCommandTimeout(ctx, timeout)
}
