package sqlite_test

// Mirrors dotnet/tests/SimpleOrm.Tests/ShadowTests.cs: a full rebuild of the
// sample migrations tree must reproduce every committed V000N.schema.json
// byte-for-byte modulo generatedAt (the model-vs-history integrity check of
// spec/migrations.md), the range form regenerates only the requested window
// and trusts the baseline, and an empty set notes plainly.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	samplemigrations "github.com/kosmassp/SimpleOrm/go/orm/sample/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

const committedSampleMigrations = "../sample/migrations"

var generatedAtPattern = regexp.MustCompile(`"generatedAt":\s*"[^"]*"`)

func blankGeneratedAt(content string) string {
	return generatedAtPattern.ReplaceAllString(content, `"generatedAt": ""`)
}

func listSchemaFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".schema.json") {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

func copySchemaFiles(t *testing.T, srcRoot, dstRoot string) {
	t.Helper()
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".schema.json") {
			return nil
		}
		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil {
			return relErr
		}
		dst := filepath.Join(dstRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o666)
	})
	if err != nil {
		t.Fatalf("copy schema files from %s: %v", srcRoot, err)
	}
}

func readNormalized(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return blankGeneratedAt(testsupport.NormalizeNewlines(string(data)))
}

func TestShadow_FullRebuildReproducesCommittedSnapshots(t *testing.T) {
	registry, err := samplemigrations.App()
	if err != nil {
		t.Fatalf("sample registry: %v", err)
	}

	outDir := testsupport.TempDir(t)
	result, err := sqlite.Shadow(context.Background(), sqlite.ShadowOptions{
		Set: registry.Migrations, Entities: registry.Entities, OutDir: outDir,
	})
	if err != nil {
		t.Fatalf("shadow: %v", err)
	}

	committed := listSchemaFiles(t, committedSampleMigrations)
	if len(result.WrittenFiles) != len(committed) {
		t.Fatalf("expected %d written files, got %d: %v", len(committed), len(result.WrittenFiles), result.WrittenFiles)
	}

	for _, relPath := range committed {
		want := readNormalized(t, filepath.Join(committedSampleMigrations, relPath))
		got := readNormalized(t, filepath.Join(outDir, relPath))
		if want != got {
			t.Fatalf("%s: regenerated snapshot differs from committed (modulo generatedAt)\nwant: %s\ngot:  %s", relPath, want, got)
		}
	}
}

func TestShadow_RangeFormRegeneratesOnlyTheGivenRangeAndTrustsTheBaseline(t *testing.T) {
	registry, err := samplemigrations.App()
	if err != nil {
		t.Fatalf("sample registry: %v", err)
	}

	outDir := testsupport.TempDir(t)
	// Seed outDir with the committed tree: the trusted baseline source for --from.
	copySchemaFiles(t, filepath.Join(committedSampleMigrations, "Table"), filepath.Join(outDir, "Table"))
	copySchemaFiles(t, filepath.Join(committedSampleMigrations, "View"), filepath.Join(outDir, "View"))

	result, err := sqlite.Shadow(context.Background(), sqlite.ShadowOptions{
		Set: registry.Migrations, Entities: registry.Entities, OutDir: outDir, From: 6, To: 9,
	})
	if err != nil {
		t.Fatalf("shadow: %v", err)
	}

	trusted := false
	for _, note := range result.Notes {
		if strings.Contains(note, "trusted") {
			trusted = true
		}
	}
	if !trusted {
		t.Fatalf("expected a 'trusted' baseline note, got: %v", result.Notes)
	}

	if len(result.WrittenFiles) == 0 {
		t.Fatal("expected some files written for versions 7-9")
	}
	for _, file := range result.WrittenFiles {
		version := versionFromSchemaFileName(t, filepath.Base(file))
		if version < 7 || version > 9 {
			t.Fatalf("expected only V0007-V0009 files, got %s", file)
		}
	}

	// The regenerated window matches the committed files it overlaps, modulo generatedAt.
	for _, relPath := range []string{
		filepath.Join("Table", "User", "V0007.schema.json"),
		filepath.Join("Table", "Role", "V0008.schema.json"),
		filepath.Join("Table", "UserProfile", "V0009.schema.json"),
	} {
		want := readNormalized(t, filepath.Join(committedSampleMigrations, relPath))
		got := readNormalized(t, filepath.Join(outDir, relPath))
		if want != got {
			t.Fatalf("%s: regenerated snapshot differs from committed (modulo generatedAt)\nwant: %s\ngot:  %s", relPath, want, got)
		}
	}
}

func versionFromSchemaFileName(t *testing.T, name string) int {
	t.Helper()
	trimmed := strings.TrimSuffix(strings.TrimPrefix(name, "V"), ".schema.json")
	version, err := strconv.Atoi(trimmed)
	if err != nil {
		t.Fatalf("unexpected snapshot file name %q: %v", name, err)
	}
	return version
}

func TestShadow_EmptySetNotesNoMigrationVersionsFound(t *testing.T) {
	set, err := migrations.NewSet()
	if err != nil {
		t.Fatalf("empty set: %v", err)
	}

	result, err := sqlite.Shadow(context.Background(), sqlite.ShadowOptions{Set: set, OutDir: testsupport.TempDir(t)})
	if err != nil {
		t.Fatalf("shadow: %v", err)
	}
	if len(result.Notes) != 1 || result.Notes[0] != "no migration versions found" {
		t.Fatalf(`expected the "no migration versions found" note, got: %v`, result.Notes)
	}
	if len(result.WrittenFiles) != 0 {
		t.Fatalf("expected no written files, got: %v", result.WrittenFiles)
	}
}
