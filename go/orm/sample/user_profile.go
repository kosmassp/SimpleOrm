package sample

import "github.com/kosmassp/SimpleOrm/go/orm"

// UserProfile is table user_profiles (STRICT) — the one-to-one fixture
// (ADR-0019 add.1): this side holds the foreign key with a unique index (the
// database is what makes a 1:1 a 1:1), so it declares an ordinary many-to-one;
// the inverse single navigation lives on User.
type UserProfile struct {
	BaseModel
	ID     int64 `orm:"column,key,generated"`
	UserID int64 `orm:"column"`
	// User is populated only by the library (Level 2 loading).
	User      *User   `orm:"many_to_one=UserID"`
	Bio       *string `orm:"column"`
	AvatarURL *string `orm:"column"`
}

func (UserProfile) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:      orm.Table("user_profiles"),
		ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[User]("UserID")},
		Indexes:     []orm.IndexDef{orm.Index("UserID").Unique()},
	}
}
