package sample

import "github.com/kosmassp/SimpleOrm/go/orm"

// User is table users (STRICT). Key: id, database-generated. Mirrors
// dotnet/samples User; export pinned by conformance/entities/user.json.
type User struct {
	BaseModel
	ID    int64  `orm:"column,key,generated"`
	Name  string `orm:"column"`
	Email string `orm:"column"`
	// DisplayName was added by migration V0002; backfilled from Name for pre-existing rows.
	DisplayName *string `orm:"column"`
	// Transactions is populated only by the library (Level 2 loading — declaration-only in this port); never a column, never written.
	Transactions []*Transaction `orm:"one_to_many=UserID"`
	// Roles resolve through the UserRole link — declared in the descriptor, never inferred (ADR-0019).
	Roles []*Role `orm:"many_to_many"`
	// Profile is the inverse side of the 1:1 — the FK (with its unique index) lives on UserProfile.
	Profile *UserProfile `orm:"one_to_one=UserID"`
}

func (User) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:     orm.Table("users"),
		Indexes:    []orm.IndexDef{orm.Index("Email").Unique(), orm.Index("DisplayName")},
		ManyToMany: []orm.ManyToManyDef{orm.ManyToMany[UserRole]("Roles")},
	}
}
