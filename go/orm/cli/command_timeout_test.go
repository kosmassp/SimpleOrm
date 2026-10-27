package cli_test

// The CLI's --command-timeout <seconds> (Level 4, ADR-0047, spec/dialects.md "Connection strings and the CLI"): a whole
// number of seconds, at least 1, for every command that opens a database; anything else refuses before anything opens.
// That the timeout reaches every statement of the tooling is covered in orm/migrations (the runner over a dialect that
// records each statement's deadline) and in orm (the session).

import (
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
)

func TestRun_CommandTimeoutRefusesAnythingButAWholeNumberOfSecondsAtLeastOne(t *testing.T) {
	for _, value := range []string{"0", "-5", "1.5", "soon", ""} {
		dbPath := testsupport.TempDatabase(t)
		_, errOut, code := runCLI(t, "migrate", "--db", dbPath, "--command-timeout", value)
		if code != 1 || !strings.Contains(errOut, "--command-timeout") {
			t.Errorf("--command-timeout %q: code=%d stderr=%s", value, code, errOut)
		}
	}
}

func TestRun_CommandTimeoutIsTakenByEveryCommandThatOpensADatabase(t *testing.T) {
	dbPath := testsupport.TempDatabase(t)
	for _, args := range [][]string{
		{"migrate", "--db", dbPath, "--command-timeout", "30"},
		{"status", "--db", dbPath, "--command-timeout", "30"},
		{"validate", "--db", dbPath, "--command-timeout", "30"},
		{"migrate", "down", "--to", "12", "--db", dbPath, "--command-timeout", "30"},
		{"baseline", "--version", "13", "--db", dbPath, "--command-timeout", "30"},
	} {
		out, errOut, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, code, out, errOut)
		}
	}
}
