package migrations

import (
	"fmt"
	"sort"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
)

// Set is a validated, ordered set of migration versions (§7.22) — the Go
// analog of MigrationRunner's discovery/validation half; a later phase builds
// the runner that executes SQL on top of it.
type Set struct {
	versions []setVersion
}

type setVersion struct {
	version Version
	number  int64
}

// NewSet builds a set from explicit versions and validates its structure
// eagerly, before any rendering: malformed root/step type names (MIG-001,
// also raised when a table/view step's type does not implement
// Action(*TableActions)/Action(*ViewActions)), two roots declaring the same
// version (MIG-002), and a step whose declared version disagrees with the
// root composing it (MIG-003).
//
// MIG-004 (a step no root composes) is unreachable here: Go has no assembly
// scanning, so a step exists in the running program only because some root's
// Compose applied it (CODING-STANDARD §10) — there is no discovered-but-
// stray candidate to check against.
func NewSet(versions ...Version) (*Set, error) {
	numbered := make([]setVersion, len(versions))
	seenRoots := map[int64]bool{}
	for i, v := range versions {
		number, err := rootVersionNumber(v)
		if err != nil {
			return nil, err
		}
		if seenRoots[number] {
			return nil, core.Errorf("MIG-002", fmt.Sprintf("V%04d", number), "more than one root migration declares this version")
		}
		seenRoots[number] = true
		numbered[i] = setVersion{version: v, number: number}
	}
	sort.Slice(numbered, func(i, j int) bool { return numbered[i].number < numbered[j].number })

	for _, nv := range numbered {
		builder := &VersionBuilder{}
		nv.version.Compose(builder)
		for _, step := range builder.Steps() {
			stepVersion, _, err := stepIdentity(step)
			if err != nil {
				return nil, err
			}
			if stepVersion != nv.number {
				return nil, core.Errorf("MIG-003", concreteTypeName(step),
					"declares version %d but is composed by V%04d", stepVersion, nv.number)
			}
			if err := checkActionShape(step); err != nil {
				return nil, err
			}
		}
	}

	return &Set{versions: numbered}, nil
}

// checkActionShape is the MIG-001 check the C# reference gets for free from
// abstract methods: a Go table/view step must implement Action itself
// (TableMigration[T]/ViewMigration[T] only declare the entity type).
func checkActionShape(step Step) error {
	switch step.describe().kind {
	case stepKindTable:
		if _, ok := step.(interface{ Action(*TableActions) }); !ok {
			return core.Errorf("MIG-001", concreteTypeName(step), "must implement Action(*orm.TableActions)")
		}
	case stepKindView:
		if _, ok := step.(interface{ Action(*ViewActions) }); !ok {
			return core.Errorf("MIG-001", concreteTypeName(step), "must implement Action(*orm.ViewActions)")
		}
	}
	return nil
}

// Versions returns the set's roots, sorted by version number.
func (s *Set) Versions() []Version {
	out := make([]Version, len(s.versions))
	for i, v := range s.versions {
		out[i] = v.version
	}
	return out
}

// VersionNumbers returns the parsed version numbers, in the same order as Versions.
func (s *Set) VersionNumbers() []int64 {
	out := make([]int64, len(s.versions))
	for i, v := range s.versions {
		out[i] = v.number
	}
	return out
}

// VersionOf returns the parsed number of v (a value == to one passed to
// NewSet), or -1 when v is not in the set.
func (s *Set) VersionOf(v Version) int64 {
	for _, sv := range s.versions {
		if sv.version == v {
			return sv.number
		}
	}
	return -1
}

// RenderedVersion is one version's rendered steps — the plan a (future)
// runner applies.
type RenderedVersion struct {
	Version int64
	Steps   []RenderedStep
}

// RenderedStep is one object's rendered change for a version: its Up
// statements, its rollback plan (empty Core means "not derivable/overridden
// here" — deriving it from snapshots is a later phase), and its declared
// renames.
type RenderedStep struct {
	Version     int64
	ObjectName  string
	Description string
	Up          []MigrationStatement
	Down        DownPlan
	Renames     []ColumnRename
}

// Render composes every version's steps against maps and dialect. A version
// composing the same object twice is MIG-002 here — it needs entity
// resolution, unreachable during NewSet's structural pass.
func (s *Set) Render(maps *metadata.Loader, dialect core.Dialect) ([]RenderedVersion, error) {
	rendered := make([]RenderedVersion, 0, len(s.versions))
	for _, nv := range s.versions {
		builder := &VersionBuilder{}
		nv.version.Compose(builder)

		seenObjects := map[string]bool{}
		steps := make([]RenderedStep, 0, len(builder.Steps()))
		for _, step := range builder.Steps() {
			desc := step.describe()
			objectName, err := objectNameFor(desc, maps)
			if err != nil {
				return nil, err
			}
			if seenObjects[objectName] {
				return nil, core.Errorf("MIG-002", fmt.Sprintf("V%04d %s", nv.number, objectName), "composed twice in one version")
			}
			seenObjects[objectName] = true

			renderedStep, err := renderStep(step, desc, objectName, maps, dialect, nv.number)
			if err != nil {
				return nil, err
			}
			steps = append(steps, renderedStep)
		}
		rendered = append(rendered, RenderedVersion{Version: nv.number, Steps: steps})
	}
	return rendered, nil
}

func objectNameFor(desc stepDescriptor, maps *metadata.Loader) (string, error) {
	if desc.kind == stepKindRaw {
		return desc.objectName, nil
	}
	m, err := maps.Load(desc.entityType)
	if err != nil {
		return "", err
	}
	return m.RelationName, nil
}

func renderStep(
	step Step, desc stepDescriptor, objectName string,
	maps *metadata.Loader, dialect core.Dialect, versionNumber int64,
) (RenderedStep, error) {
	_, description, err := stepIdentity(step)
	if err != nil {
		return RenderedStep{}, err
	}

	switch desc.kind {
	case stepKindTable:
		return renderTableStep(step, desc, objectName, description, maps, dialect, versionNumber)
	case stepKindView:
		return renderViewStep(step, desc, objectName, description, maps, dialect, versionNumber)
	default:
		return renderRawStep(desc, objectName, description, versionNumber), nil
	}
}

func renderTableStep(
	step Step, desc stepDescriptor, objectName, description string,
	maps *metadata.Loader, dialect core.Dialect, versionNumber int64,
) (RenderedStep, error) {
	m, err := maps.Load(desc.entityType)
	if err != nil {
		return RenderedStep{}, err
	}
	if m.Kind != core.RelationTable {
		return RenderedStep{}, core.Errorf(
			"DDL-001", core.TypeName(desc.entityType), "is %s-backed; a table step applies to tables", m.Kind)
	}

	action, ok := step.(interface{ Action(*TableActions) })
	if !ok {
		return RenderedStep{}, core.Errorf("MIG-001", concreteTypeName(step), "must implement Action(*orm.TableActions)")
	}
	up := newTableActions(m, dialect)
	action.Action(up)

	pre, post := &MigrationSQL{}, &MigrationSQL{}
	if hook, ok := step.(PreDowner); ok {
		hook.PreDown(pre)
	}
	if hook, ok := step.(PostDowner); ok {
		hook.PostDown(post)
	}
	down := newTableActions(m, dialect)
	if hook, ok := step.(TableDowner); ok {
		hook.Down(down)
	}

	return RenderedStep{
		Version:     versionNumber,
		ObjectName:  objectName,
		Description: description,
		Up:          up.build(),
		Down:        DownPlan{Pre: pre.render("pre-down"), Core: down.build(), Post: post.render("post-down")},
		Renames:     up.ColumnRenames(),
	}, nil
}

func renderViewStep(
	step Step, desc stepDescriptor, objectName, description string,
	maps *metadata.Loader, dialect core.Dialect, versionNumber int64,
) (RenderedStep, error) {
	m, err := maps.Load(desc.entityType)
	if err != nil {
		return RenderedStep{}, err
	}
	if m.Kind != core.RelationView && m.Kind != core.RelationMaterializedView {
		return RenderedStep{}, core.Errorf(
			"DDL-001", core.TypeName(desc.entityType), "is %s-backed; a view step applies to views", m.Kind)
	}
	if m.Kind == core.RelationMaterializedView && !dialect.SupportsMaterializedViews() {
		return RenderedStep{}, core.Errorf("DDL-002", core.TypeName(desc.entityType), "the dialect has no materialized views")
	}

	action, ok := step.(interface{ Action(*ViewActions) })
	if !ok {
		return RenderedStep{}, core.Errorf("MIG-001", concreteTypeName(step), "must implement Action(*orm.ViewActions)")
	}
	up := newViewActions(m, dialect)
	action.Action(up)

	pre, post := &MigrationSQL{}, &MigrationSQL{}
	if hook, ok := step.(PreDowner); ok {
		hook.PreDown(pre)
	}
	if hook, ok := step.(PostDowner); ok {
		hook.PostDown(post)
	}
	down := newViewActions(m, dialect)
	if hook, ok := step.(ViewDowner); ok {
		hook.Down(down)
	}

	return RenderedStep{
		Version:     versionNumber,
		ObjectName:  objectName,
		Description: description,
		Up:          up.build(),
		Down:        DownPlan{Pre: pre.render("pre-down"), Core: down.build(), Post: post.render("post-down")},
	}, nil
}

func renderRawStep(desc stepDescriptor, objectName, description string, versionNumber int64) RenderedStep {
	var up []MigrationStatement
	if desc.expectDefinition != "" {
		up = append(up, MigrationStatement{
			SQL: NormalizeDDL(desc.expectDefinition), Origin: "expect " + objectName, GuardView: objectName,
		})
	}
	for _, sql := range desc.up {
		up = append(up, MigrationStatement{SQL: sql, Origin: "sql " + objectName})
	}
	var down []MigrationStatement
	for _, sql := range desc.down {
		down = append(down, MigrationStatement{SQL: sql, Origin: "sql " + objectName})
	}

	return RenderedStep{
		Version:     versionNumber,
		ObjectName:  objectName,
		Description: description,
		Up:          up,
		Down:        DownPlan{Core: down},
		Renames:     desc.renames,
	}
}
