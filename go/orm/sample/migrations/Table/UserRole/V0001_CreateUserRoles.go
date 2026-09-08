// Package userrole holds the user_roles table's migration steps, mirroring
// dotnet/samples/SimpleOrm.Sample/Migrations/Table/UserRole (CLAUDE.md §7.22).
package userrole

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0001_CreateUserRoles is frozen to literal SQL when V0005 changed the table
// (ADR-0013/0016): the shape user_roles had at V0001.
type V0001_CreateUserRoles struct {
	orm.TableMigration[sample.UserRole]
}

func (V0001_CreateUserRoles) Action(actions *orm.TableActions) {
	actions.SQL(`create table if not exists user_roles (
    user_id     INTEGER NOT NULL,
    role_id     INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT,
    primary key (user_id, role_id)
) STRICT`)
	actions.SQL("create index if not exists ix_user_roles_role_id on user_roles (role_id)")
}
