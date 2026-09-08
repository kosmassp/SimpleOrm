package amendfixture

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	amendwidget "github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture/Table/AmendWidget"
)

type V0001 struct{}

func (V0001) Compose(version *orm.VersionBuilder) {
	version.Apply(amendwidget.V0001_Create{})
}
