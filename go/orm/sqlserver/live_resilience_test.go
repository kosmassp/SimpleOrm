package sqlserver_test

// The resilience leg of the SQL Server dialect (Level 4, ADR-0047, spec/dialects.md "Testing contract"): a statement
// outrunning a one-second CommandTimeout surfaces the driver's own error and the session's next statement runs. Skips
// where no server answers (SIMPLEORM_SQLSERVER); UNTESTED until the owner's engine is back.

import (
	"context"
	"errors"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"

	"github.com/kosmassp/SimpleOrm/go/orm"
)

func TestLive_CommandTimeoutSurfacesTheDriversErrorAndTheNextStatementRuns(t *testing.T) {
	db := openLive(t, newLiveDatabase(t), orm.Options{CommandTimeout: time.Second})
	ctx := context.Background()

	started := time.Now()
	_, err := orm.Execute(ctx, db, orm.InlineCommand[orm.EmptyArgs]("waitfor delay '00:00:03'"), orm.EmptyArgs{})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("waitfor delay '00:00:03' under a one-second timeout succeeded")
	}
	if elapsed >= 2500*time.Millisecond {
		t.Fatalf("the timeout fired after %s; expected about one second", elapsed)
	}
	// go-mssqldb sends an attention on the context's end and reports the context's error; a driver reporting the
	// server's -2 instead is accepted too.
	var serverErr mssql.Error
	if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &serverErr) && serverErr.Number == -2) {
		t.Fatalf("expected the driver's timeout, got %T: %v", err, err)
	}
	if ctx.Err() != nil {
		t.Fatal("the caller's context ended; the timeout must be the session's own")
	}

	one, err := orm.QuerySingle(ctx, db, orm.Inline[orm.EmptyArgs, int64]("select cast(1 as bigint)"), orm.EmptyArgs{})
	if err != nil || one != 1 {
		t.Fatalf("the next statement after the timeout: %v, %v", one, err)
	}
}
