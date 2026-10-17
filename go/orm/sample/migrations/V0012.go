package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/UserProfile"
)

// V0012 is the fixture's bytes column (ADR-0040 add.1): user_profiles gains avatar.
type V0012 struct{}

func (V0012) Compose(version *orm.VersionBuilder) {
	version.Apply(userprofile.V0012_AddAvatar{})
}
