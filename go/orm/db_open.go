package orm

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// openConnection is the open of spec/session.md "Resilience" (Level 4, ADR-0047), after the options were validated:
// for attempt 0, 1, 2, … the dialect creates a fresh pool, one connection is pinned from it and pinged; on success the
// session owns both. On failure both are released, and the open is attempted again only when attempt < OpenRetries,
// the caller's context is live, the failure is not a cancellation, and the dialect calls it transient — otherwise the
// failure is returned as it was raised (the last attempt's, when retries were spent). Between two attempts it waits
// OpenRetryDelay, and a context that ends during the wait ends the open with the context's error and no further
// attempt.
//
// The dialect refusing the connection string itself (CreateConnection's error: pgx's or msdsn's parser) is not an
// open attempt and is never retried — it would fail the same way every time (CODING-STANDARD §10). Nothing is logged:
// an open attempt sends no statement.
func openConnection(ctx context.Context, connectionString string, options Options) (*sql.DB, *sql.Conn, error) {
	delay := options.OpenRetryDelay
	if delay == 0 {
		delay = DefaultOpenRetryDelay
	}
	for attempt := 0; ; attempt++ {
		pool, err := options.Dialect.CreateConnection(connectionString)
		if err != nil {
			return nil, nil, err
		}
		connection, err := pinConnection(ctx, pool)
		if err == nil {
			return pool, connection, nil
		}
		_ = pool.Close()

		if attempt >= options.OpenRetries || ctx.Err() != nil || errors.Is(err, context.Canceled) || !options.Dialect.IsTransient(err) {
			return nil, nil, err
		}
		if err := waitBeforeRetry(ctx, delay); err != nil {
			return nil, nil, err
		}
	}
}

// pinConnection opens one connection of pool and pings it: the open of one attempt. A failed ping releases the connection.
func pinConnection(ctx context.Context, pool *sql.DB) (*sql.Conn, error) {
	connection, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if err := connection.PingContext(ctx); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

// waitBeforeRetry waits delay, or returns the context's error as soon as it ends. NoOpenRetryDelay does not wait.
func waitBeforeRetry(ctx context.Context, delay time.Duration) error {
	if delay <= NoOpenRetryDelay {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
