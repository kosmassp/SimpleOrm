// Package testsupport is the shared test plumbing (CODING-STANDARD §7): a real
// temp-file SQLite database per test (ADR-0003 — never a mock, never
// in-memory) and the location of the shared conformance/ tree (§9).
package testsupport

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TempDatabase returns the path of a fresh SQLite database file under the OS
// temp directory and deletes it (with its -wal/-shm/-journal siblings) when
// the test ends. The caller opens it; nothing is created here.
func TempDatabase(t testing.TB) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	path := filepath.Join(os.TempDir(), "simpleorm_go_"+hex.EncodeToString(suffix[:])+".db")
	t.Cleanup(func() {
		// Best effort: Windows can briefly hold a delete lock after the driver closes its handle.
		for _, file := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
			_ = os.Remove(file)
		}
	})
	return path
}

// TempDir is a fresh directory removed when the test ends (t.TempDir with a stable name prefix).
func TempDir(t testing.TB) string {
	t.Helper()
	return t.TempDir()
}

// ConformanceDir locates the shared conformance/ tree by walking up from the
// working directory (the package directory under `go test`). A candidate must
// hold the `entities` folder: the runners' own package is also named
// conformance, and a bare name match would stop there.
func ConformanceDir(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "conformance")
		if info, err := os.Stat(filepath.Join(candidate, "entities")); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("conformance/ not found above %s — run from the repository checkout", dir)
		}
		dir = parent
	}
}

// ConformanceFolder is the path of one conformance folder (entities, cases, …).
func ConformanceFolder(t testing.TB, folder string) string {
	t.Helper()
	return filepath.Join(ConformanceDir(t), folder)
}

// ConformanceCases lists the *.json file names in a conformance folder, sorted.
func ConformanceCases(t testing.TB, folder string) []string {
	t.Helper()
	entries, err := os.ReadDir(ConformanceFolder(t, folder))
	if err != nil {
		t.Fatalf("read conformance/%s: %v", folder, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// ReadConformance reads one conformance file.
func ReadConformance(t testing.TB, folder string, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ConformanceFolder(t, folder), name))
	if err != nil {
		t.Fatalf("read conformance/%s/%s: %v", folder, name, err)
	}
	return data
}

// NormalizeNewlines maps CRLF to LF: a Windows checkout can rewrite the pinned
// files' line endings, and byte comparisons must not depend on that.
func NormalizeNewlines(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}
