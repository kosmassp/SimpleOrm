package userprofile

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0010_AddAddress is the owned Address of UserProfile (ADR-0030) as three
// nullable columns — nullable because the navigation is: an all-NULL row
// reads back as no address. No Down — the runner derives the rollback from
// the snapshots (ADR-0018).
type V0010_AddAddress struct {
	orm.TableMigration[sample.UserProfile]
}

func (V0010_AddAddress) Action(actions *orm.TableActions) {
	actions.AddColumn("address_street", "TEXT")
	actions.AddColumn("address_city", "TEXT")
	actions.AddColumn("address_postal_code", "TEXT")
}
