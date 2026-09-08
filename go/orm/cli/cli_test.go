package cli_test

// Mirrors php/tests/Cli/ApplicationTest.php against the sample registry
// (sample/migrations.App()): one end-to-end workflow exercising every
// database command in sequence, plus the argument-handling edge cases.

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/cli"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	samplemigrations "github.com/kosmassp/SimpleOrm/go/orm/sample/migrations"
)

const committedSampleMigrations = "../sample/migrations"

func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	registry, err := samplemigrations.App()
	if err != nil {
		t.Fatalf("sample registry: %v", err)
	}
	var out, errBuf bytes.Buffer
	code = cli.Run(context.Background(), registry, args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

func TestRun_FullWorkflowAgainstTheSampleRegistry(t *testing.T) {
	dbPath := testsupport.TempDatabase(t)

	// migrate: applies every version on a fresh database.
	out, errOut, code := runCLI(t, "migrate", "--db", dbPath)
	if code != 0 {
		t.Fatalf("migrate: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "applied 9 version(s)") {
		t.Fatalf("expected 9 applied versions, got: %s", out)
	}

	// status: every step Applied.
	out, errOut, code = runCLI(t, "status", "--db", dbPath)
	if code != 0 {
		t.Fatalf("status: code=%d stderr=%s", code, errOut)
	}
	assertEveryLineContains(t, out, "Applied")

	// migrate again: idempotent, nothing pending.
	out, errOut, code = runCLI(t, "migrate", "--db", dbPath)
	if code != 0 || !strings.Contains(out, "nothing pending") {
		t.Fatalf("expected 'nothing pending', got code=%d out=%s stderr=%s", code, out, errOut)
	}

	// validate: SchemaGuard reports a clean database.
	out, errOut, code = runCLI(t, "validate", "--db", dbPath)
	if code != 0 {
		t.Fatalf("validate: code=%d stderr=%s", code, errOut)
	}
	if strings.TrimSpace(out) != "valid" {
		t.Fatalf(`expected "valid", got: %s`, out)
	}

	// export-metadata --out: byte-identical to conformance/entities/*.json.
	exportDir := testsupport.TempDir(t)
	_, errOut, code = runCLI(t, "export-metadata", "--out", exportDir)
	if code != 0 {
		t.Fatalf("export-metadata: code=%d stderr=%s", code, errOut)
	}
	assertExportedMetadataMatchesConformance(t, exportDir)

	// snapshot --out: byte-identical (modulo generatedAt) to the committed tree.
	snapshotDir := testsupport.TempDir(t)
	_, errOut, code = runCLI(t, "snapshot", "--db", dbPath, "--out", snapshotDir)
	if code != 0 {
		t.Fatalf("snapshot: code=%d stderr=%s", code, errOut)
	}
	assertSnapshotsMatchCommitted(t, snapshotDir)

	// diff --out <copy of the committed snapshots>: no schema changes.
	diffDir := testsupport.TempDir(t)
	copySchemaFiles(t, filepath.Join(committedSampleMigrations, "Table"), filepath.Join(diffDir, "Table"))
	copySchemaFiles(t, filepath.Join(committedSampleMigrations, "View"), filepath.Join(diffDir, "View"))
	out, errOut, code = runCLI(t, "diff", "--out", diffDir)
	if code != 0 {
		t.Fatalf("diff: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "no schema changes") {
		t.Fatalf("expected 'no schema changes', got: %s", out)
	}

	// migrate down --to 0 --snapshots <that dir>: reverts everything.
	out, errOut, code = runCLI(t, "migrate", "down", "--to", "0", "--db", dbPath, "--snapshots", diffDir)
	if code != 0 {
		t.Fatalf("migrate down: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "reverted 9 version(s)") {
		t.Fatalf("expected 9 reverted versions, got: %s", out)
	}

	out, errOut, code = runCLI(t, "status", "--db", dbPath)
	if code != 0 {
		t.Fatalf("status after rollback: code=%d stderr=%s", code, errOut)
	}
	assertEveryLineContains(t, out, "Pending")

	// baseline --version 9: records history without running anything.
	out, errOut, code = runCLI(t, "baseline", "--version", "9", "--db", dbPath)
	if code != 0 {
		t.Fatalf("baseline: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "baselined at V0009") {
		t.Fatalf("expected 'baselined at V0009', got: %s", out)
	}
}

func TestRun_UnknownDialectExits1(t *testing.T) {
	_, errOut, code := runCLI(t, "status", "--db", testsupport.TempDatabase(t), "--dialect", "mysql")
	if code != 1 {
		t.Fatalf("expected exit 1 for an unknown dialect, got %d (stderr=%s)", code, errOut)
	}
	if errOut == "" {
		t.Fatal("expected an error message naming the unknown dialect")
	}
}

func TestRun_NoArgsExits2WithUsage(t *testing.T) {
	out, _, code := runCLI(t)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(out, "simpleorm") || !strings.Contains(out, "migrate") {
		t.Fatalf("expected the usage text, got: %s", out)
	}
}

func TestRun_UnknownCommandExits2WithUsage(t *testing.T) {
	out, _, code := runCLI(t, "frobnicate")
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(out, "simpleorm") {
		t.Fatalf("expected the usage text, got: %s", out)
	}
}

func TestRun_MissingDBExits1(t *testing.T) {
	_, errOut, code := runCLI(t, "status")
	if code != 1 {
		t.Fatalf("expected exit 1 for a missing --db, got %d (stderr=%s)", code, errOut)
	}
}

// --- assertions and fixtures --------------------------------------------------

func assertEveryLineContains(t *testing.T, output, substring string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		t.Fatal("expected at least one status line")
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		if !strings.Contains(line, substring) {
			t.Fatalf("expected every line to contain %q, got: %s", substring, line)
		}
	}
}

func assertExportedMetadataMatchesConformance(t *testing.T, dir string) {
	t.Helper()
	conformanceDir := testsupport.ConformanceFolder(t, "entities")
	entries, err := os.ReadDir(conformanceDir)
	if err != nil {
		t.Fatalf("read conformance/entities: %v", err)
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		count++
		want := strings.TrimRight(testsupport.NormalizeNewlines(readFileString(t, filepath.Join(conformanceDir, entry.Name()))), "\n")
		got := strings.TrimRight(testsupport.NormalizeNewlines(readFileString(t, filepath.Join(dir, entry.Name()))), "\n")
		if want != got {
			t.Fatalf("%s: exported metadata differs from the conformance file", entry.Name())
		}
	}
	if count != 10 {
		t.Fatalf("expected 10 conformance entity files, found %d", count)
	}
}

var generatedAtPattern = regexp.MustCompile(`"generatedAt":\s*"[^"]*"`)

func blankGeneratedAt(content string) string {
	return generatedAtPattern.ReplaceAllString(content, `"generatedAt": ""`)
}

// assertSnapshotsMatchCommitted checks the CLI's `snapshot --out`, which
// writes exactly one file per object — at the last version touching it, per
// Status (mirrors Program.cs's SnapshotAsync) — against the corresponding
// highest-versioned file already committed for that object.
func assertSnapshotsMatchCommitted(t *testing.T, dir string) {
	t.Helper()
	latest := latestSchemaFilePerObject(t, committedSampleMigrations)
	written := listSchemaFiles(t, dir)

	if len(written) != len(latest) {
		t.Fatalf("expected %d written snapshot(s), got %d: %v", len(latest), len(written), written)
	}
	for i := range latest {
		if written[i] != latest[i] {
			t.Fatalf("expected snapshot files %v, got %v", latest, written)
		}
	}

	for _, relPath := range latest {
		want := blankGeneratedAt(testsupport.NormalizeNewlines(readFileString(t, filepath.Join(committedSampleMigrations, relPath))))
		got := blankGeneratedAt(testsupport.NormalizeNewlines(readFileString(t, filepath.Join(dir, relPath))))
		if want != got {
			t.Fatalf("%s: written snapshot differs from committed (modulo generatedAt)\nwant: %s\ngot:  %s", relPath, want, got)
		}
	}
}

// latestSchemaFilePerObject picks, per object directory, the highest-versioned
// committed snapshot file — what a fresh `snapshot --out` reproduces.
func latestSchemaFilePerObject(t *testing.T, root string) []string {
	t.Helper()
	bestVersion := map[string]int{}
	bestFile := map[string]string{}
	for _, rel := range listSchemaFiles(t, root) {
		dir := filepath.Dir(rel)
		version := versionFromSchemaFileName(t, filepath.Base(rel))
		if current, ok := bestVersion[dir]; !ok || version > current {
			bestVersion[dir] = version
			bestFile[dir] = rel
		}
	}
	result := make([]string, 0, len(bestFile))
	for _, rel := range bestFile {
		result = append(result, rel)
	}
	sort.Strings(result)
	return result
}

func versionFromSchemaFileName(t *testing.T, name string) int {
	t.Helper()
	trimmed := strings.TrimSuffix(strings.TrimPrefix(name, "V"), ".schema.json")
	version := 0
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			t.Fatalf("unexpected snapshot file name %q", name)
		}
		version = version*10 + int(r-'0')
	}
	return version
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

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
