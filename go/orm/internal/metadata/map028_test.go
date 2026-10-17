package metadata_test

import (
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// MAP-028 (ADR-0041) exists for languages with subclassing. Go has no
// abstract types and no scan rule (embedding is composition), so only one
// analog is reachable: method promotion. An embedded struct that declares
// Entity() — its own relation source or indexes — would be promoted onto the
// embedding struct, which would then silently adopt the embedded struct's
// table; the loader refuses it instead, naming both types.

type Map028Base struct {
	ID int64 `orm:"column,key,generated"`
}

func (Map028Base) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("Map028_bases")}
}

// Map028Outer declares no descriptor of its own: promotion would hand it Map028Base's table.
type Map028Outer struct {
	Map028Base
	Title string `orm:"column"`
}

// Map028OuterWithOwn declares its own descriptor; the embedded one is still a relation source.
type Map028OuterWithOwn struct {
	Map028Base
	Title string `orm:"column"`
}

func (Map028OuterWithOwn) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("Map028_owns")}
}

// Map028Deep embeds the outer, so the base is two levels down.
type Map028Deep struct {
	Map028Outer
}

type Map028IndexedBase struct {
	ID int64 `orm:"column,key,generated"`
	N  int64 `orm:"column"`
}

func (Map028IndexedBase) Entity() orm.EntityDef {
	return orm.EntityDef{Indexes: []orm.IndexDef{orm.Index("N")}}
}

type Map028IndexedOuter struct {
	Map028IndexedBase
}

// Map028PlainBase has no descriptor: the sample's BaseModel shape.
type Map028PlainBase struct {
	Stamp int64 `orm:"column=stamp"`
}

type Map028PlainOuter struct {
	ID int64 `orm:"column,key,generated"`
	Map028PlainBase
}

func (Map028PlainOuter) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("Map028_plain")}
}

func assertMAP028Names(t *testing.T, err error, names ...string) {
	t.Helper()
	if err == nil || !core.HasCode(err, "MAP-028") {
		t.Fatalf("expected MAP-028, got %v", err)
	}
	for _, n := range names {
		if !strings.Contains(err.Error(), n) {
			t.Errorf("the MAP-028 error must name %s: %v", n, err)
		}
	}
}

func TestEmbeddedStructWithRelationSource_IsMAP028(t *testing.T) {
	_, err := metadata.Load[Map028Outer](metadata.NewLoader(nil))
	assertMAP028Names(t, err, "Map028Outer", "Map028Base")
}

func TestEmbeddedStructWithRelationSource_IsMAP028_EvenWithOwnDescriptor(t *testing.T) {
	_, err := metadata.Load[Map028OuterWithOwn](metadata.NewLoader(nil))
	assertMAP028Names(t, err, "Map028OuterWithOwn", "Map028Base")
}

func TestEmbeddedStructWithRelationSource_IsMAP028_ThroughNestedEmbedding(t *testing.T) {
	_, err := metadata.Load[Map028Deep](metadata.NewLoader(nil))
	assertMAP028Names(t, err, "Map028Deep", "Map028Base")
}

func TestEmbeddedStructWithIndexes_IsMAP028(t *testing.T) {
	_, err := metadata.Load[Map028IndexedOuter](metadata.NewLoader(nil))
	assertMAP028Names(t, err, "Map028IndexedOuter", "Map028IndexedBase")
}

// The embedded struct on its own is still a loadable struct: embedding is
// composition, so refusing the outer does not make the embedded one a
// "base class" in any other sense.
func TestEmbeddedStructWithDescriptor_StillLoadsOnItsOwn(t *testing.T) {
	m, err := metadata.Load[Map028Base](metadata.NewLoader(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.RelationName != "Map028_bases" {
		t.Errorf("relation = %q", m.RelationName)
	}
}

// A plain embedded struct (no descriptor) — the sample's BaseModel — shares
// its fields and nothing else: its columns land on the embedding struct's
// own table, and the outer's descriptor is the outer's.
func TestPlainEmbeddedStruct_SharesFieldsOnly(t *testing.T) {
	m, err := metadata.Load[Map028PlainOuter](metadata.NewLoader(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.RelationName != "Map028_plain" {
		t.Errorf("relation = %q", m.RelationName)
	}
	if m.Property("Stamp") == nil {
		t.Error("the embedded struct's field must map on the embedding struct")
	}
}

func TestSampleEntitiesEmbedBaseModelWithoutAdoptingIt(t *testing.T) {
	m, err := metadata.Load[sample.Role](metadata.NewLoader(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.RelationName != "roles" {
		t.Errorf("relation = %q", m.RelationName)
	}
}
