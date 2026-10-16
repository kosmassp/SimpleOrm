package role

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0011_AddIsDeleted adds the soft-delete flag of Role (Level 3 M3, spec/hooks.md) as a NOT NULL column
// defaulting to 0, so the existing rows are live. No Down — the runner derives the rollback from the
// snapshots (ADR-0018).
type V0011_AddIsDeleted struct {
	orm.TableMigration[sample.Role]
}

func (V0011_AddIsDeleted) Action(actions *orm.TableActions) {
	actions.AddColumn("is_deleted", "INTEGER", orm.NotNull("0"))
}
