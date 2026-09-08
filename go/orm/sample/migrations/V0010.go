package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/UserProfile"
)

// V0010 is the owned-type columns (ADR-0030): user_profiles gains address_*
// for the owned Address.
type V0010 struct{}

func (V0010) Compose(version *orm.VersionBuilder) {
	version.Apply(userprofile.V0010_AddAddress{})
}
