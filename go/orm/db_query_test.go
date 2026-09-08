package orm_test

import (
	"context"
	"embed"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

//go:embed testdata/sql/get_user_by_email.sql
var embeddedSQL embed.FS

type emailArgs struct {
	Email string
}

type userEmailRow struct {
	ID    int64
	Email string
}

var allUsersInline = orm.Inline[orm.EmptyArgs, sample.User](
	"select id, name, email, display_name, created_at, updated_at from users order by id")

var countUsers = orm.Inline[orm.EmptyArgs, int64]("select count(id) from users")

func TestQuery_MapsEntitiesThroughTheirEntityMap(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")

	users, err := orm.QueryAll[sample.User](ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].Name != "Ada" {
		t.Errorf("expected Ada first, got %s", users[0].Name)
	}
	if !users[0].CreatedAtUtc.Equal(testsupport.SeedTime) {
		t.Errorf("expected seed time, got %v", users[0].CreatedAtUtc)
	}
	if users[0].CreatedAtUtc.Location() != testsupport.SeedTime.Location() {
		t.Errorf("expected UTC location round trip")
	}
	if users[0].UpdatedAtUtc != nil {
		t.Errorf("expected nil UpdatedAtUtc")
	}
}

func TestScalarAndDTOResults_SharePipeline(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	count, err := orm.QuerySingle(ctx, db, countUsers, orm.EmptyArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1, got %d", count)
	}

	emailRows := orm.Inline[orm.EmptyArgs, userEmailRow]("select id, email from users order by id")
	rows, err := orm.Query(ctx, db, emailRows, orm.EmptyArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Email != "ada@example.com" {
		t.Fatalf("got %+v", rows)
	}
}

func TestQuerySingle_EnforcesRowCountsWithCodes(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	_, err := orm.QuerySingle(ctx, db, allUsersInline, orm.EmptyArgs{})
	if orm.CodeOf(err) != "QRY-001" {
		t.Fatalf("expected QRY-001, got %v", err)
	}

	missing, err := orm.GetOrDefault[sample.User](ctx, db, int64(999))
	if err != nil || missing != nil {
		t.Fatalf("expected nil, nil, got %v, %v", missing, err)
	}

	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")

	_, err = orm.QuerySingle(ctx, db, allUsersInline, orm.EmptyArgs{})
	if orm.CodeOf(err) != "QRY-002" {
		t.Fatalf("expected QRY-002, got %v", err)
	}
}

func TestEmbedded_SQLStaysSupportedAsTheOptionalForm(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	byEmail := orm.Embedded[emailArgs, sample.User](embeddedSQL, "testdata/sql/get_user_by_email.sql")
	user, err := orm.QuerySingle(ctx, db, byEmail, emailArgs{Email: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if user.Name != "Ada" {
		t.Errorf("expected Ada, got %s", user.Name)
	}
}

func TestResultColumnWithNoMappedProperty_IsMAP001(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	bad := orm.Inline[orm.EmptyArgs, sample.User](
		"select id, name, email, created_at, updated_at, 42 as mystery from users")
	_, err := orm.Query(ctx, db, bad, orm.EmptyArgs{})
	if orm.CodeOf(err) != "MAP-001" {
		t.Fatalf("expected MAP-001, got %v", err)
	}
}
