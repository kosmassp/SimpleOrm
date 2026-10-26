package cli_test

// The CLI's --log (Level 4 item 4, ADR-0045, spec/session.md "Statement log"): every statement of a migrate, migrate
// down, shadow, validate, or force-sync run goes to standard output, one line each, named for the tool that sent it.

import (
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
)

// namedLines are the lines of out that start with prefix, so a command's own messages ("applied 13 version(s)") are told apart from its statements.
func namedLines(out, prefix string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestRun_LogWritesEveryMigrationStatementNamedMigrateAndNeverTheRunLockOrTransactionControl(t *testing.T) {
	dbPath := testsupport.TempDatabase(t)

	quiet, _, code := runCLI(t, "migrate", "--db", dbPath+"-quiet")
	if code != 0 || strings.Contains(quiet, "migrate: ") {
		t.Fatalf("without --log no statement line is written: code=%d %s", code, quiet)
	}

	out, errOut, code := runCLI(t, "migrate", "--db", dbPath, "--log")
	if code != 0 {
		t.Fatalf("migrate --log: code=%d stderr=%s", code, errOut)
	}
	lines := namedLines(out, "migrate: ")
	if len(lines) == 0 {
		t.Fatalf("migrate --log writes its statements: %s", out)
	}
	var inserts int
	for _, line := range lines {
		if strings.Contains(line, "insert into schema_version") {
			inserts++
			if !strings.HasSuffix(line, "[@version, @object, @description, @checksum, @applied_at, @execution_ms]") {
				t.Errorf("the history insert names its parameters, never its values: %s", line)
			}
		}
		for _, housekeeping := range []string{"begin", "commit", "rollback", "savepoint"} {
			if strings.HasPrefix(line, "migrate: "+housekeeping) {
				t.Errorf("transaction control is not logged: %s", line)
			}
		}
	}
	if inserts == 0 {
		t.Error("the recorded history is among the logged statements")
	}
	if !strings.Contains(out, "applied 13 version(s)") {
		t.Errorf("the command's own output still follows: %s", out)
	}

	out, errOut, code = runCLI(t, "migrate", "down", "--to", "12", "--db", dbPath, "--log")
	if code != 0 {
		t.Fatalf("migrate down --log: code=%d stderr=%s", code, errOut)
	}
	if len(namedLines(out, "migrate: ")) == 0 {
		t.Errorf("the rollback's statements are logged as migrate: %s", out)
	}
}

func TestRun_LogNamesValidateShadowAndSyncStatementsForTheirTool(t *testing.T) {
	dbPath := testsupport.TempDatabase(t)
	if _, errOut, code := runCLI(t, "migrate", "--db", dbPath); code != 0 {
		t.Fatalf("migrate: %s", errOut)
	}

	out, errOut, code := runCLI(t, "validate", "--db", dbPath, "--log")
	if code != 0 {
		t.Fatalf("validate --log: code=%d stderr=%s", code, errOut)
	}
	if len(namedLines(out, "validate ")) == 0 || len(namedLines(out, "migrate: ")) == 0 {
		t.Errorf("SchemaGuard writes validate lines and the MIG-030 check reads the history as migrate: %s", out)
	}

	out, errOut, code = runCLI(t, "migrate", "--force", "--db", dbPath, "--log")
	if code != 0 {
		t.Fatalf("migrate --force --log: code=%d stderr=%s", code, errOut)
	}
	if len(namedLines(out, "sync: ")) == 0 {
		t.Errorf("the force-sync introspection is logged as sync: %s", out)
	}

	out, errOut, code = runCLI(t, "shadow", "--out", testsupport.TempDir(t), "--log")
	if code != 0 {
		t.Fatalf("shadow --log: code=%d stderr=%s", code, errOut)
	}
	shadow := namedLines(out, "shadow: ")
	if len(shadow) == 0 {
		t.Fatalf("the replay and its introspection are logged as shadow: %s", out)
	}
	if len(namedLines(out, "migrate: ")) != 0 {
		t.Errorf("the replay runner yields its name to shadow: %v", namedLines(out, "migrate: "))
	}
}
