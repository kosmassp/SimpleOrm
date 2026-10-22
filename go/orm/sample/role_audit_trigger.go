package sample

import "github.com/kosmassp/SimpleOrm/go/orm"

// RoleAuditTrigger is the fixture trigger (Level 4, ADR-0042): after a role is updated, one role_changes row
// per updated row records its id, name, and the UTC instant. Role links it. It is a declaration class, never
// an entity — no columns, no key; the one neutral body renders per dialect (SQLite and PostgreSQL row
// triggers, SQL Server's statement trigger over `inserted`).
type RoleAuditTrigger struct{}

func (RoleAuditTrigger) Trigger() orm.TriggerDef {
	return orm.TriggerDef{
		Name:   "trg_roles_audit",
		Timing: orm.TimingAfter,
		Events: orm.EventUpdate,
		SQL: `insert into role_changes (role_id, role_name, changed_at)
select n.id, n.role_name, {now}
from {new: id, role_name} n;`,
	}
}
