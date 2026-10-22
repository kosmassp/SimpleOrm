package conformance

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

// snapshotCase is conformance/snapshot-cases/*.json (conformance/README.md): a fixture entity, version, and
// pinned generation time in, the exact exported snapshot document out — per dialect since ADR-0043: `expect`
// is keyed by dialect name, every dialect the port ships exports the entity and must produce its document
// exactly, and a shipped dialect missing from `expect` fails the suite.
type snapshotCase struct {
	Name        string                     `json:"name"`
	Entity      string                     `json:"entity"`
	Kind        string                     `json:"kind"`
	Trigger     string                     `json:"trigger"`
	AsOfVersion int64                      `json:"asOfVersion"`
	GeneratedAt string                     `json:"generatedAt"`
	Expect      map[string]json.RawMessage `json:"expect"`
}

func TestSnapshotCases_MatchExpectedOutput(t *testing.T) {
	for _, file := range testsupport.ConformanceCases(t, "snapshot-cases") {
		t.Run(file, func(t *testing.T) {
			data := testsupport.ReadConformance(t, "snapshot-cases", file)
			var c snapshotCase
			if err := json.Unmarshal(data, &c); err != nil {
				t.Fatalf("parse case: %v", err)
			}

			entityType, ok := entityTypeNamed(c.Entity)
			if !ok {
				t.Fatalf("no fixture entity named %q", c.Entity)
			}
			m, err := metadata.NewLoader(nil).Load(entityType)
			if err != nil {
				t.Fatalf("load %s: %v", c.Entity, err)
			}
			generatedAt, err := time.Parse(time.RFC3339Nano, c.GeneratedAt)
			if err != nil {
				t.Fatalf("parse generatedAt: %v", err)
			}

			names := make([]string, 0, len(astDialects))
			for name := range astDialects {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				t.Run(name, func(t *testing.T) {
					rawExpected, present := c.Expect[name]
					if !present {
						t.Fatalf("the case pins no document for the shipped dialect %q", name)
					}
					dialect := astDialects[name]()
					if dialect.Name() != name {
						t.Fatalf("the dialect registered as %q names itself %q", name, dialect.Name())
					}

					var produced string
					switch {
					case c.Kind == "trigger":
						// A trigger snapshots by declaration (ADR-0042): the linked trigger the case names; neutral,
						// but stamped for every dialect but sqlite (ADR-0043).
						var linked *core.EntityTrigger
						for _, trigger := range m.Triggers {
							if trigger.Definition.Name == c.Trigger {
								linked = trigger
							}
						}
						if linked == nil {
							t.Fatalf("%s links no trigger named %q", c.Entity, c.Trigger)
						}
						declaration := migrations.DeclarationFromEntity(m, linked)
						declaration.Dialect = dialect.Name()
						produced = migrations.ExportTrigger(declaration, c.AsOfVersion, generatedAt)
					case c.Kind != "" && c.Kind != "table":
						produced = migrations.ExportDDL(m.RelationName, dialect.Name(), c.Kind, dialect.CreateViewSQL(m), c.AsOfVersion, generatedAt)
					default:
						produced = migrations.Export(m, dialect, c.AsOfVersion, generatedAt)
					}

					var expected, actual any
					if err := json.Unmarshal(rawExpected, &expected); err != nil {
						t.Fatalf("parse expect: %v", err)
					}
					if err := json.Unmarshal([]byte(produced), &actual); err != nil {
						t.Fatalf("parse produced: %v\n%s", err, produced)
					}
					if !reflect.DeepEqual(expected, actual) {
						t.Errorf("produced snapshot differs from the expected document:\n%s", produced)
					}
				})
			}
		})
	}
}

// entityTypeNamed resolves a fixture entity by its unqualified type name (the
// case format's "entity"), the way the C# reference resolves it by scanning
// the sample assembly's exported types.
func entityTypeNamed(name string) (reflect.Type, bool) {
	for _, t := range entityTypes {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}
