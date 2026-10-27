package core

import (
	"context"
	"time"
)

// The command timeout (Level 4, ADR-0047, spec/session.md "Resilience"): a bound on how long one statement may run.
// database/sql has no per-command timeout property; the driver's equivalent is the statement's context deadline, so the
// option is realized as context.WithTimeout derived from the caller's context around each statement the library sends
// (CODING-STANDARD §10). CommandContext is the one place that applies it: the session calls it with its own option, and
// the tooling (the migration runner, the shadow, force-sync, SchemaGuard's introspection) — which cannot take a session
// parameter without widening its public surface — reads the value WithCommandTimeout attached to the context, the way
// it reaches the statement log.
//
// A timeout that fires surfaces as the driver (or database/sql) returns it — an error for which
// errors.Is(err, context.DeadlineExceeded) holds, or the engine's own cancellation error — while the caller's own
// context is still live; the caller's cancellation or deadline is the caller's (ctx.Err() is set). Nothing here retries,
// rolls back, or closes anything.

// CommandTimeoutSeconds is the timeout a statement carries for the option value: whole seconds rounded up, at least 1
// (0.2 s → 1 s, 1.5 s → 2 s), as the other implementations hand the drivers an integer number of seconds. Zero is Go's
// spelling of unset (the driver's default, untouched) and stays zero.
func CommandTimeoutSeconds(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 0
	}
	seconds := (timeout + time.Second - 1) / time.Second
	if seconds < 1 {
		seconds = 1
	}
	return seconds * time.Second
}

// CommandContext is the context one statement runs under: ctx itself when timeout is unset (zero — nothing derived,
// nothing allocated), else ctx with a deadline CommandTimeoutSeconds(timeout) from now. The caller defers the cancel
// once the statement — and its result set, when it has one — is done with.
func CommandContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, noCancel
	}
	return context.WithTimeout(ctx, CommandTimeoutSeconds(timeout))
}

func noCancel() {}

type commandTimeoutKey struct{}

// WithCommandTimeout attaches the session's (or the CLI's) command timeout to ctx for the tooling; zero returns ctx
// unchanged.
func WithCommandTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if timeout <= 0 {
		return ctx
	}
	return context.WithValue(ctx, commandTimeoutKey{}, timeout)
}

// CommandTimeoutFrom is the timeout attached to ctx, or zero.
func CommandTimeoutFrom(ctx context.Context) time.Duration {
	timeout, _ := ctx.Value(commandTimeoutKey{}).(time.Duration)
	return timeout
}

// StatementContext is CommandContext with the timeout ctx carries: the tooling's form.
func StatementContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return CommandContext(ctx, CommandTimeoutFrom(ctx))
}

// SendStatement is the one call every tooling site makes as it sends a statement: it writes the statement's log line
// (LogStatement) and returns the context the statement runs under (StatementContext). The caller defers the cancel.
func SendStatement(ctx context.Context, sqlText string, args ...any) (context.Context, context.CancelFunc) {
	LogStatement(ctx, sqlText, args...)
	return StatementContext(ctx)
}
