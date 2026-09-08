// Package models is the entity leaf of the amend-cases fixture — split from
// the amendfixture package the same way orm/sample is split from
// orm/sample/migrations: the step package (Table/AmendWidget) must import the
// entity type, and the root package (amendfixture) must import the step
// package, so the entity type cannot live in the root package too without an
// import cycle (a Go constraint the C#/PHP ports don't hit).
package models

import "github.com/kosmassp/SimpleOrm/go/orm"

// AmendWidget is the conformance/amend-cases fixture entity (ADR-0017 add.3,
// conformance/amend-cases/README.md), matching
// dotnet/tests/SimpleOrm.Tests/AmendFixtures.cs and
// php/tests/Conformance/AmendFixture exactly: the model has moved on to
// Remark since the draft V0002 (which added note) was generated — that
// mismatch is the point of the fixture.
type AmendWidget struct {
	ID     int64   `orm:"column,key,generated"`
	Name   string  `orm:"column"`
	Remark *string `orm:"column"`
}

func (AmendWidget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("amend_widgets")}
}
