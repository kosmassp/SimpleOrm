// Package roleaudittrigger holds the RoleAuditTrigger declaration's migration steps (Level 4, ADR-0042),
// mirroring dotnet/samples/SimpleOrm.Sample/Migrations/Trigger/RoleAuditTrigger (CLAUDE.md §7.22).
package roleaudittrigger

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0013_CreateRoleAuditTrigger creates the trigger from the entity's linked declaration, rendered through the
// dialect (drop if present, then create) — legal for an object's initial creation, frozen thereafter by the
// checksum. No Down — the runner derives the rollback from the snapshots (ADR-0018): at the creating version,
// the drop.
type V0013_CreateRoleAuditTrigger struct {
	orm.TriggerMigration[sample.Role, sample.RoleAuditTrigger]
}

func (V0013_CreateRoleAuditTrigger) Action(actions *orm.TriggerActions) {
	actions.CreateTrigger()
}
