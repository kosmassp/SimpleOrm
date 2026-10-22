// Package rolechange holds the role_changes table's migration step (Level 4, ADR-0042), mirroring
// dotnet/samples/SimpleOrm.Sample/Migrations/Table/RoleChange (CLAUDE.md §7.22).
package rolechange

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0013_CreateRoleChanges creates the table RoleAuditTrigger writes to, rendered from metadata (legal for an
// object's initial creation, frozen thereafter by the checksum). No Down — the runner derives the rollback
// from the snapshots (ADR-0018).
type V0013_CreateRoleChanges struct {
	orm.TableMigration[sample.RoleChange]
}

func (V0013_CreateRoleChanges) Action(actions *orm.TableActions) {
	actions.CreateTable()
}
