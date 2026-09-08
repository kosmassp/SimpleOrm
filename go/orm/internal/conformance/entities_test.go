package conformance

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// entityTypes are the ten fixture entities of dotnet/samples/SimpleOrm.Sample,
// mirrored one to one in orm/sample (CODING-STANDARD §7).
var entityTypes = []reflect.Type{
	reflect.TypeFor[sample.User](),
	reflect.TypeFor[sample.Role](),
	reflect.TypeFor[sample.UserRole](),
	reflect.TypeFor[sample.Transaction](),
	reflect.TypeFor[sample.TransactionDetail](),
	reflect.TypeFor[sample.UserProfile](),
	reflect.TypeFor[sample.UserTransactionTotal](),
	reflect.TypeFor[sample.MonthlySalesTotal](),
	reflect.TypeFor[sample.DailySales](),
	reflect.TypeFor[sample.UserActivityReport](),
}

// TestEntities_MatchConformanceFiles is the conformance runner for entity
// metadata (CLAUDE.md §9): every fixture entity's export must be
// byte-identical (modulo line-ending normalization) to
// conformance/entities/<snake_case name>.json — the file every port
// reproduces.
func TestEntities_MatchConformanceFiles(t *testing.T) {
	loader := metadata.NewLoader(nil)
	dir := testsupport.ConformanceFolder(t, "entities")
	covered := map[string]bool{}

	for _, entityType := range entityTypes {
		entityType := entityType
		fileName := core.ToSnakeCase(entityType.Name()) + ".json"
		covered[fileName] = true

		t.Run(entityType.Name(), func(t *testing.T) {
			m, err := loader.Load(entityType)
			if err != nil {
				t.Fatalf("load %s: %v", entityType.Name(), err)
			}

			actual := testsupport.NormalizeNewlines(metadata.Export(m))
			expectedBytes, err := os.ReadFile(filepath.Join(dir, fileName))
			if err != nil {
				t.Fatalf("read conformance/entities/%s: %v", fileName, err)
			}
			expected := strings.TrimRight(testsupport.NormalizeNewlines(string(expectedBytes)), "\n")

			if actual != expected {
				t.Errorf("export mismatch for %s (conformance/entities/%s):\n--- expected ---\n%s\n--- actual ---\n%s",
					entityType.Name(), fileName, expected, actual)
			}
		})
	}

	t.Run("every conformance file is covered by a case", func(t *testing.T) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read conformance/entities: %v", err)
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			if !covered[name] {
				t.Errorf("conformance/entities/%s has no matching entity type in this runner", name)
			}
		}
	})
}
