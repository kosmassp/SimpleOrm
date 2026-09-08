package orm_test

import (
	"context"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

func TestStream_YieldsRowsLazily(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")

	var names []string
	for user, err := range orm.Stream(ctx, db, allUsersInline, orm.EmptyArgs{}) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, user.Name)
	}

	if len(names) != 2 || names[0] != "Ada" || names[1] != "Grace" {
		t.Fatalf("got %v", names)
	}
}

func TestStream_MAP002FiresBeforeTheFirstRow(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	partial := orm.Inline[orm.EmptyArgs, sample.User]("select id, name from users")

	seen := 0
	var streamErr error
	for _, err := range orm.Stream(ctx, db, partial, orm.EmptyArgs{}) {
		seen++
		streamErr = err
		break
	}

	if seen != 1 {
		t.Fatalf("expected exactly one yield (the error), got %d", seen)
	}
	if orm.CodeOf(streamErr) != "MAP-002" {
		t.Fatalf("expected MAP-002, got %v", streamErr)
	}
}

func TestStream_StopsEarlyWithoutReadingEveryRow(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	testsupport.InsertUser(t, db, "Edsger", "edsger@example.com")

	count := 0
	for _, err := range orm.Stream(ctx, db, allUsersInline, orm.EmptyArgs{}) {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if count == 1 {
			break
		}
	}

	if count != 1 {
		t.Fatalf("expected the loop to stop after 1, got %d", count)
	}
}

func TestStream_CancelledContextEndsTheSequence(t *testing.T) {
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sawError := false
	for _, err := range orm.Stream(ctx, db, allUsersInline, orm.EmptyArgs{}) {
		if err != nil {
			sawError = true
		}
	}

	if !sawError {
		t.Fatalf("expected the cancelled context to surface as an error")
	}
}
