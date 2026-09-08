// Package user holds the users table's migration steps, mirroring
// dotnet/samples/SimpleOrm.Sample/Migrations/Table/User (CLAUDE.md §7.22).
package user

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0001_CreateUsers is frozen to literal SQL (ADR-0013): metadata-rendered
// creates are only safe while the object never changes again — V0002 adds
// display_name, so V0001 must stay the shape users had *then*, not whatever
// the entity looks like today. This is the freeze the diff generator performs
// automatically when it emits a follow-up migration for an object.
type V0001_CreateUsers struct {
	orm.TableMigration[sample.User]
}

func (V0001_CreateUsers) Action(actions *orm.TableActions) {
	actions.SQL(`create table if not exists users (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    email       TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT
) STRICT`)
	actions.SQL("create unique index if not exists ix_users_email on users (email)")
}
