// Package role holds the roles table's migration steps, mirroring
// dotnet/samples/SimpleOrm.Sample/Migrations/Table/Role (CLAUDE.md §7.22).
package role

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0001_CreateRoles is frozen to literal SQL when V0004 renamed the column
// (ADR-0013/0016): the shape roles had at V0001. Its Post hook seeds data —
// seed data belongs to the create action, not a separate mechanism.
type V0001_CreateRoles struct {
	orm.TableMigration[sample.Role]
}

func (V0001_CreateRoles) Action(actions *orm.TableActions) {
	actions.SQL(`create table if not exists roles (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT
) STRICT`).Post("insert into roles (name, created_at) values ('admin', '2026-01-01T00:00:00.0000000Z')")
}
