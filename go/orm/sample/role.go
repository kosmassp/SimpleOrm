package sample

import "github.com/kosmassp/SimpleOrm/go/orm"

// Role is table roles (STRICT). Key: id, database-generated.
type Role struct {
	BaseModel
	ID int64 `orm:"column,key,generated"`
	// Name's column was renamed to role_name by migration V0004; renamed_from keeps that declared rename with the
	// model (Level 3 M5) -- permanent and harmless once V0004 is in the history.
	Name string `orm:"column=role_name,renamed_from=name"`
	// IsDeleted is the soft-delete flag (Level 3 M3, spec/hooks.md): a delete sets it instead of removing the
	// row; added by migration V0011.
	IsDeleted bool `orm:"column,soft_delete"`
	// Users is the reverse side of User.Roles, through the same link (ADR-0019).
	Users []*User `orm:"many_to_many"`
}

func (Role) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:     orm.Table("roles"),
		Indexes:    []orm.IndexDef{orm.Index("Name").Unique()},
		ManyToMany: []orm.ManyToManyDef{orm.ManyToMany[UserRole]("Users")},
		// RoleAuditTrigger writes one role_changes row per updated role (Level 4, ADR-0042).
		Triggers: []orm.TriggerLink{orm.Trigger[RoleAuditTrigger]()},
	}
}
