package conformance

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// snapshotCase is conformance/snapshot-cases/*.json (conformance/README.md):
// a fixture entity, version, and pinned generation time in, the exact
// exported snapshot document out (mirrors ConformanceSnapshotTests.cs).
type snapshotCase struct {
	Name        string          `json:"name"`
	Entity      string          `json:"entity"`
	Kind        string          `json:"kind"`
	AsOfVersion int64           `json:"asOfVersion"`
	GeneratedAt string          `json:"generatedAt"`
	Expect      json.RawMessage `json:"expect"`
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
			loader := metadata.NewLoader(nil)
			m, err := loader.Load(entityType)
			if err != nil {
				t.Fatalf("load %s: %v", c.Entity, err)
			}
			dialect := sqlite.New()
			generatedAt, err := time.Parse(time.RFC3339Nano, c.GeneratedAt)
			if err != nil {
				t.Fatalf("parse generatedAt: %v", err)
			}

			var produced string
			if c.Kind != "" && c.Kind != "table" {
				produced = migrations.ExportDDL(m.RelationName, c.Kind, dialect.CreateViewSQL(m), c.AsOfVersion, generatedAt)
			} else {
				produced = migrations.Export(m, dialect, c.AsOfVersion, generatedAt)
			}

			var expected, actual any
			if err := json.Unmarshal(c.Expect, &expected); err != nil {
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
