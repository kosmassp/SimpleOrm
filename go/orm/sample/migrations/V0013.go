package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/RoleChange"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Trigger/RoleAuditTrigger"
)

// V0013 is the first trigger in the mapping (Level 4, ADR-0042): the role_changes table, then the trigger on
// roles that writes to it — triggers compose last, because their relation must exist.
type V0013 struct{}

func (V0013) Compose(version *orm.VersionBuilder) {
	version.
		Apply(rolechange.V0013_CreateRoleChanges{}).
		Apply(roleaudittrigger.V0013_CreateRoleAuditTrigger{})
}
