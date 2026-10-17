package testsupport

import (
	"bytes"
	"context"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// The bytes values every dialect round-trips (ADR-0040 add.1): a NUL, a backslash, 0xff, and an
// invalid UTF-8 sequence, then bytes spelling a bytea escape literal (`\x41\\` + NUL) — what a
// text binding would corrupt.
var (
	BytesFirst  = []byte{0x00, 0x5c, 0xff, 0x27, 0x41, 0xc3, 0x28, 0x0a}
	BytesSecond = []byte{0x5c, 0x78, 0x34, 0x31, 0x5c, 0x5c, 0x00}
)

type avatarArgs struct {
	Avatar []byte
}

var profilesByAvatar = orm.Inline[avatarArgs, sample.UserProfile](
	"select id, user_id, bio, avatar_url, avatar, address_street, address_city, address_postal_code, created_at, updated_at" +
		" from user_profiles where avatar = @Avatar")

// BytesRoundTrip writes UserProfile.Avatar by insert, a column-list update, and a full update,
// matches it by a criteria query and a registry query, and ends with a NULL into the bytes
// column, on a session holding the sample tables.
func BytesRoundTrip(t *testing.T, db *orm.Db) {
	t.Helper()
	ctx := context.Background()
	user := InsertUser(t, db, "Ada", "ada@example.com")
	first := append([]byte(nil), BytesFirst...)
	profile := &sample.UserProfile{UserID: user.ID, Avatar: &first}
	profile.CreatedAtUtc = SeedTime
	if err := orm.Insert(ctx, db, profile); err != nil {
		t.Fatalf("insert: %v", err)
	}
	expectAvatar(t, db, profile.ID, BytesFirst)

	byCriteria, err := orm.From[sample.UserProfile](db).Where(orm.Eq("Avatar", BytesFirst)).List(ctx)
	if err != nil || len(byCriteria) != 1 || byCriteria[0].ID != profile.ID {
		t.Fatalf("criteria: %+v, %v", byCriteria, err)
	}
	byQuery, err := orm.Query(ctx, db, profilesByAvatar, avatarArgs{Avatar: BytesFirst})
	if err != nil || len(byQuery) != 1 || byQuery[0].ID != profile.ID {
		t.Fatalf("registry query: %+v, %v", byQuery, err)
	}

	second := append([]byte(nil), BytesSecond...)
	profile.Avatar = &second
	if err := orm.UpdateOnly(ctx, db, profile, "Avatar"); err != nil {
		t.Fatalf("update only: %v", err)
	}
	expectAvatar(t, db, profile.ID, BytesSecond)

	profile.Avatar = nil
	if err := orm.Update(ctx, db, profile); err != nil {
		t.Fatalf("update with a NULL avatar: %v", err)
	}
	expectAvatar(t, db, profile.ID, nil)
}

func expectAvatar(t *testing.T, db *orm.Db, id int64, want []byte) {
	t.Helper()
	read, err := orm.Get[sample.UserProfile](context.Background(), db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	switch {
	case want == nil && read.Avatar != nil:
		t.Fatalf("expected a NULL avatar, got %x", *read.Avatar)
	case want != nil && (read.Avatar == nil || !bytes.Equal(*read.Avatar, want)):
		t.Fatalf("expected avatar %x, got %v", want, read.Avatar)
	}
}
