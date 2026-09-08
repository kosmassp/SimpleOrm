package user

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0002_AddDisplayName is a real change migration: literal column spec
// (frozen forever), with the per-action Post hook backfilling existing rows —
// data work rides the same version atomicity as the DDL.
//
// No hand-written Down (owner decision, ADR-0016): rollbacks derive from the
// versioned schema snapshots (diff V0002 vs V0001) once a generator exists;
// until then, migrate down refuses honestly with MIG-020.
type V0002_AddDisplayName struct {
	orm.TableMigration[sample.User]
}

func (V0002_AddDisplayName) Action(actions *orm.TableActions) {
	actions.AddColumn("display_name", "TEXT").Post("update users set display_name = name")
}
