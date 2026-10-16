package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/Role"
)

// V0011 is the soft-delete flag (Level 3 M3, spec/hooks.md): roles gain is_deleted for the [SoftDelete]
// hook of Role.
type V0011 struct{}

func (V0011) Compose(version *orm.VersionBuilder) {
	version.Apply(role.V0011_AddIsDeleted{})
}
