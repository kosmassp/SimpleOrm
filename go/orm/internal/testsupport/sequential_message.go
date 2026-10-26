package testsupport

import (
	"context"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// SequentialMessage is a StatementBatcher's SendMessage realized with plain statements on the session's executor, one
// after another, every statement of the message run before any result is judged — which is the contract a one-round-trip
// realization has (spec/session.md "Batching": "the statements after a failing check may have run"). It lets the
// session's batched branch (a message handed to the dialect, a result per statement handed back and completed in order)
// be exercised against a real SQLite database: the RecordingDialect and the ObservingDialect use it when asked to
// behave as a batching dialect, and the conformance runners then replay the batch and flush cases through it.
func SequentialMessage(ctx context.Context, exec core.SQLExecutor, statements []core.MessageStatement) ([]core.MessageResult, error) {
	results := make([]core.MessageResult, len(statements))
	log := core.StatementLogFrom(ctx) // the realization writes the lines, one per statement, before the message goes out
	for _, statement := range statements {
		log.Statement(statement.Name, statement.SQL, statement.Args)
	}
	for i, statement := range statements {
		switch statement.Result {
		case core.ResultKey, core.ResultReportedCount:
			rows, err := exec.QueryContext(ctx, statement.SQL, statement.Args...)
			if err != nil {
				return nil, err
			}
			var value any
			if rows.Next() {
				err = rows.Scan(&value)
			}
			if err == nil {
				err = rows.Err()
			}
			_ = rows.Close()
			if err != nil {
				return nil, err
			}
			if statement.Result == core.ResultKey {
				results[i].Key = value
			} else if count, ok := value.(int64); ok {
				results[i].Affected = count
			}
		case core.ResultDriverCount:
			result, err := exec.ExecContext(ctx, statement.SQL, statement.Args...)
			if err != nil {
				return nil, err
			}
			if results[i].Affected, err = result.RowsAffected(); err != nil {
				return nil, err
			}
		default: // core.ResultNone
			if _, err := exec.ExecContext(ctx, statement.SQL, statement.Args...); err != nil {
				return nil, err
			}
		}
	}
	return results, nil
}
