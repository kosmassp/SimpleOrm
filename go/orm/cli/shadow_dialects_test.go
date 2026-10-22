package cli_test

import (
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
)

// `shadow` accepts every dialect (ADR-0043): on sqlserver and postgres --db is the admin connection the
// throwaway database is created on, so it is required there; SQLite ignores it. The live runs — a PostgreSQL
// tree replayed through the command — are in internal/conformance (shadow_round_trip_test.go).

func TestRun_ShadowOnAnUnknownDialectRefusesBeforeAnythingOpens(t *testing.T) {
	_, errOut, code := runCLI(t, "shadow", "--dialect", "mysql", "--out", testsupport.TempDir(t))
	if code != 1 || !strings.Contains(errOut, "unknown --dialect 'mysql'") {
		t.Fatalf("expected exit 1 naming the unknown dialect, got code %d, stderr %q", code, errOut)
	}
}

func TestRun_ShadowOnANonSqliteDialectNeedsTheAdminConnection(t *testing.T) {
	for _, dialect := range []string{"postgres", "sqlserver"} {
		_, errOut, code := runCLI(t, "shadow", "--dialect", dialect, "--out", testsupport.TempDir(t))
		if code != 1 || !strings.Contains(errOut, "admin connection") {
			t.Errorf("%s: expected exit 1 asking for the admin connection, got code %d, stderr %q", dialect, code, errOut)
		}
	}
}

func TestRun_ShadowOnSqliteIgnoresDb(t *testing.T) {
	// A sqlite shadow needs no --db: the throwaway database is a temp file of its own.
	out, errOut, code := runCLI(t, "shadow", "--out", testsupport.TempDir(t), "--to", "V0001")
	if code != 0 {
		t.Fatalf("shadow: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "regenerated") {
		t.Fatalf("expected regenerated snapshots, got: %s", out)
	}
}
