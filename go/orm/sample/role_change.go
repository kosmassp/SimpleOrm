package sample

import (
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
)

// RoleChange is table role_changes (STRICT) — the table RoleAuditTrigger writes one row to for every updated
// role (Level 4, ADR-0042, migration V0013). It is not an audit-hook model: the trigger fills ChangedAt, never
// the session. Key: id, database-generated.
type RoleChange struct {
	ID        int64     `orm:"column,key,generated"`
	RoleID    int64     `orm:"column"`
	RoleName  string    `orm:"column"`
	ChangedAt time.Time `orm:"column"`
}

func (RoleChange) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("role_changes")}
}
