package amendfixture

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	amendwidget "github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture/Table/AmendWidget"
)

type V0002 struct{}

func (V0002) Compose(version *orm.VersionBuilder) {
	version.Apply(amendwidget.V0002_AddNote{})
}
