package userprofile

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0012_AddAvatar is the avatar image of UserProfile (ADR-0040 add.1) — the
// fixture's one bytes column, nullable, so binary round trips are covered on
// every dialect. No Down — the runner derives the rollback from the snapshots
// (ADR-0018).
type V0012_AddAvatar struct {
	orm.TableMigration[sample.UserProfile]
}

func (V0012_AddAvatar) Action(actions *orm.TableActions) {
	actions.AddColumn("avatar", "BLOB")
}
