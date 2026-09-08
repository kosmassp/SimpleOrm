package role

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0005_SeedUserRole is a data-only step: no DDL, just rows — the same
// mechanism, the same atomicity. A (future) derived rollback sees an
// unchanged schema and reverts nothing, which is correct for structure — the
// seeded row is data, so the PreDown hook carries its removal (data work is
// always hook territory, never derived).
type V0005_SeedUserRole struct {
	orm.TableMigration[sample.Role]
}

func (V0005_SeedUserRole) Action(actions *orm.TableActions) {
	actions.SQL("insert into roles (role_name, created_at) values ('user', '2026-08-29T00:00:00.0000000Z')")
}

func (V0005_SeedUserRole) PreDown(sql *orm.MigrationSQL) {
	sql.SQL("delete from roles where role_name = 'user'")
}
