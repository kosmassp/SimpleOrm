package sample

import "github.com/kosmassp/SimpleOrm/go/orm"

// UserRole is table user_roles (STRICT): composite natural key (user_id,
// role_id), neither part database-generated — the composite-key fixture (§7.4)
// and the many-to-many link between users and roles. CreatedAtUtc from the
// base doubles as the assignment timestamp.
type UserRole struct {
	BaseModel
	UserID int64 `orm:"column,key"`
	RoleID int64 `orm:"column,key"`
	// GrantedBy was added by migration V0005.
	GrantedBy *string `orm:"column"`
	// User is populated only by the library (Level 2 loading); it can never disagree with UserID.
	User *User `orm:"many_to_one=UserID"`
	// Role is populated only by the library (Level 2 loading); it can never disagree with RoleID.
	Role *Role `orm:"many_to_one=RoleID"`
}

func (UserRole) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:      orm.Table("user_roles"),
		ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[User]("UserID"), orm.ForeignKey[Role]("RoleID")},
		Indexes:     []orm.IndexDef{orm.Index("RoleID")},
	}
}
