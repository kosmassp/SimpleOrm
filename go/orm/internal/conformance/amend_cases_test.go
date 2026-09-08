package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture/models"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// amendFixturePackage is the import path amendfixture's step packages live
// under (Table/AmendWidget's real package path) — what DiffOptions.Package
// names, and what a generated root's imports are built from.
const amendFixturePackage = "github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture"

// generatedStepStub and handWrittenStub are file contents laid out for a
// step file: only the presence/absence of the generator's header matters to
// the amend command, so a stub in place of a real, compilable step is enough
// (mirrors ConformanceAmendTests.cs / AmendCasesTest.php).
const generatedStepStub = "// " + migrations.GeneratedMarker + " (ADR-0017); conformance stub\npackage stub\n"
const handWrittenStub = "// hand-written: raw SQL nobody can diff back into existence\npackage stub\n"

type amendCase struct {
	Name      string           `json:"name"`
	Fixture   string           `json:"fixture"`
	Files     []amendFileSpec  `json:"files"`
	Snapshots []amendSnapshot  `json:"snapshots"`
	Command   amendCommandSpec `json:"command"`
	Expect    amendExpectSpec  `json:"expect"`
}

type amendFileSpec struct {
	Path      string `json:"path"`
	Generated bool   `json:"generated"`
	Dialect   string `json:"dialect"`
}

type amendSnapshot struct {
	Path     string          `json:"path"`
	Snapshot json.RawMessage `json:"snapshot"`
}

type amendCommandSpec struct {
	Amend   bool   `json:"amend"`
	Force   bool   `json:"force"`
	Name    string `json:"name"`
	Dialect string `json:"dialect"`
	Applied bool   `json:"applied"`
}

type amendExpectSpec struct {
	Exit    int               `json:"exit"`
	Deleted []string          `json:"deleted"`
	Written []string          `json:"written"`
	Kept    []string          `json:"kept"`
	Files   []amendFileExpect `json:"files"`
	Output  []string          `json:"output"`
	Error   []string          `json:"error"`
}

type amendFileExpect struct {
	Path     string   `json:"path"`
	Contains []string `json:"contains"`
	Omits    []string `json:"omits"`
}

// amendPath is a case's extension-less source path, adapted to this port's
// .go extension (amend-cases/README: "each implementation appends its own").
// Snapshot paths already carry an extension and pass through unchanged.
func amendPath(dir, relative string) string {
	native := filepath.FromSlash(relative)
	if filepath.Ext(native) == "" {
		native += ".go"
	}
	return filepath.Join(dir, native)
}

func TestAmendCases_BehaveAsSpecified(t *testing.T) {
	for _, file := range testsupport.ConformanceCases(t, "amend-cases") {
		t.Run(file, func(t *testing.T) {
			data := testsupport.ReadConformance(t, "amend-cases", file)
			var spec amendCase
			if err := json.Unmarshal(data, &spec); err != nil {
				t.Fatalf("parse case: %v", err)
			}

			dir := t.TempDir()
			layAmendFiles(t, dir, spec)

			var stdout, stderr bytes.Buffer
			exit := runAmendDiff(t, dir, spec, &stdout, &stderr)

			checkAmendExpect(t, dir, spec.Expect, exit, stdout.String(), stderr.String())
		})
	}
}

// layAmendFiles lays out the case's "before" tree: root/step source files
// (generated or hand-written) and snapshot documents at their literal paths.
func layAmendFiles(t *testing.T, dir string, spec amendCase) {
	t.Helper()

	var stepRefs []migrations.RootStepRef
	for _, f := range spec.Files {
		if !strings.Contains(f.Path, "/") {
			continue
		}
		stepRefs = append(stepRefs, amendStepRef(f.Path))
	}

	for _, f := range spec.Files {
		isRoot := !strings.Contains(f.Path, "/")
		var content string
		switch {
		case f.Generated && isRoot:
			version := amendRootVersion(t, f.Path)
			dialectLabel := f.Dialect
			if dialectLabel == "" {
				dialectLabel = "sqlite"
			}
			source, err := migrations.EmitRoot(amendFixturePackage, version, stepRefs, dialectLabel)
			if err != nil {
				t.Fatalf("emit root fixture %s: %v", f.Path, err)
			}
			content = source
		case f.Generated && !isRoot:
			content = generatedStepStub
		default:
			content = handWrittenStub
		}

		path := amendPath(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatalf("mkdir for %s: %v", f.Path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
			t.Fatalf("write %s: %v", f.Path, err)
		}
	}

	for _, snapshot := range spec.Snapshots {
		path := filepath.Join(dir, filepath.FromSlash(snapshot.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatalf("mkdir for snapshot %s: %v", snapshot.Path, err)
		}
		if err := os.WriteFile(path, snapshot.Snapshot, 0o666); err != nil {
			t.Fatalf("write snapshot %s: %v", snapshot.Path, err)
		}
	}
}

// amendStepRef turns a case step path ("Table/AmendWidget/V0002_AddNote")
// into the RootStepRef a generated root composes: the real package the
// amendfixture's Table/AmendWidget directory declares is "amendwidget"
// (lowercase of the entity type name, CODING-STANDARD §10's EmitTableStep rule).
func amendStepRef(path string) migrations.RootStepRef {
	parts := strings.Split(path, "/")
	folder, typeName, className := parts[0], parts[1], parts[2]
	return migrations.RootStepRef{
		ImportPath: amendFixturePackage + "/" + folder + "/" + typeName,
		Package:    strings.ToLower(typeName),
		TypeName:   className,
	}
}

// amendRootVersion parses "V0002" (a root file's extension-less path) into 2.
func amendRootVersion(t *testing.T, path string) int64 {
	t.Helper()
	version, err := strconv.ParseInt(strings.TrimPrefix(path, "V"), 10, 64)
	if err != nil {
		t.Fatalf("parse root version from %q: %v", path, err)
	}
	return version
}

func runAmendDiff(t *testing.T, dir string, spec amendCase, stdout, stderr *bytes.Buffer) int {
	t.Helper()

	empty := spec.Fixture == "none"
	set, err := amendfixture.Set()
	if empty {
		set, err = amendfixture.None()
	}
	if err != nil {
		t.Fatalf("build fixture set: %v", err)
	}

	var entityTypes []reflect.Type
	if !empty {
		entityTypes = []reflect.Type{reflect.TypeFor[models.AmendWidget]()}
	}

	dialectLabel := spec.Command.Dialect
	if dialectLabel == "" {
		dialectLabel = "sqlite"
	}

	var isApplied func(context.Context, int64) (bool, error)
	if spec.Command.Applied {
		isApplied = func(context.Context, int64) (bool, error) { return true, nil }
	}

	return migrations.ExecuteDiff(context.Background(), migrations.DiffOptions{
		Set:          set,
		EntityTypes:  entityTypes,
		OutDir:       dir,
		Package:      amendFixturePackage,
		Dialect:      sqlite.New(),
		DialectLabel: dialectLabel,
		Name:         spec.Command.Name,
		Amend:        spec.Command.Amend,
		Force:        spec.Command.Force,
		IsApplied:    isApplied,
	}, stdout, stderr)
}

func checkAmendExpect(t *testing.T, dir string, expect amendExpectSpec, exit int, stdout, stderr string) {
	t.Helper()

	if exit != expect.Exit {
		t.Fatalf("exit: want %d, got %d\nstdout=%s\nstderr=%s", expect.Exit, exit, stdout, stderr)
	}

	for _, relative := range expect.Deleted {
		if _, err := os.Stat(amendPath(dir, relative)); err == nil {
			t.Errorf("%s should have been deleted", relative)
		}
	}
	for _, relative := range append(append([]string(nil), expect.Written...), expect.Kept...) {
		if _, err := os.Stat(amendPath(dir, relative)); err != nil {
			t.Errorf("%s should exist: %v", relative, err)
		}
	}

	for _, file := range expect.Files {
		data, err := os.ReadFile(amendPath(dir, file.Path))
		if err != nil {
			t.Fatalf("read %s: %v", file.Path, err)
		}
		content := string(data)
		for _, fragment := range file.Contains {
			if !strings.Contains(content, fragment) {
				t.Errorf("%s should contain %q:\n%s", file.Path, fragment, content)
			}
		}
		for _, fragment := range file.Omits {
			if strings.Contains(content, fragment) {
				t.Errorf("%s should not contain %q:\n%s", file.Path, fragment, content)
			}
		}
	}

	for _, fragment := range expect.Output {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("stdout should contain %q, got %q", fragment, stdout)
		}
	}
	for _, fragment := range expect.Error {
		if !strings.Contains(stderr, fragment) {
			t.Errorf("stderr should contain %q, got %q", fragment, stderr)
		}
	}

	if exit == 0 && stderr != "" {
		t.Errorf("expected no stderr on exit 0, got %q", stderr)
	}
}
