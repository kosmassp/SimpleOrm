package migrations

import (
	"context"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The statement log of the migration tooling (Level 4 item 4, ADR-0045, spec/session.md "Statement log"). The runner, the
// shadow, force-sync, and the live-schema readers take no session, so they reach the log through the context:
// WithStatementLog attaches the sink, and every statement they send is then written as one line named `migrate` (the
// runner: the version table, the recorded history, the steps, the guards' definition reads, the rollback), `shadow`
// (the replay, its introspection, the view and trigger read-back), or `sync` (the force-sync introspection and the
// plan's statements). Without it nothing is written and nothing is formatted. Not logged, being the dialect's own
// housekeeping and transaction control: the run lock, the savepoints of the view probe, begin/commit/rollback, and the
// shadow host's create/drop of the throwaway database.

// WithStatementLog returns ctx with the sink attached: write receives one line per statement the migration tooling
// sends under it, parameter values included when values is set. A nil write returns ctx unchanged.
func WithStatementLog(ctx context.Context, write func(line string), values bool) context.Context {
	return core.WithStatementLog(ctx, core.NewStatementLog(write, values))
}
