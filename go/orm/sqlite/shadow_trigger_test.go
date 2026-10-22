package sqlite_test

// The shadow replay over triggers (Level 4, ADR-0042, spec/migrations.md "Snapshots"): a trigger's snapshot from
// history is the stored SQLite trigger parsed back, byte-identical (modulo generatedAt) to the one written from
// metadata; the trusted baseline restores triggers from their snapshots, rendered through the dialect.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	samplemigrations "github.com/kosmassp/SimpleOrm/go/orm/sample/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

func seededOutDir(t *testing.T) string {
	t.Helper()
	outDir := testsupport.TempDir(t)
	for _, kind := range []string{"Table", "View", "Trigger"} {
		copySchemaFiles(t, filepath.Join(committedSampleMigrations, kind), filepath.Join(outDir, kind))
	}
	return outDir
}

func TestShadow_ATriggerSnapshotFromHistoryIsTheStoredTriggerParsedBack(t *testing.T) {
	registry, err := samplemigrations.App()
	if err != nil {
		t.Fatal(err)
	}
	outDir := seededOutDir(t)

	result, err := sqlite.Shadow(context.Background(), sqlite.ShadowOptions{
		Set: registry.Migrations, Entities: registry.Entities, Triggers: registry.Triggers, OutDir: outDir, From: 12, To: 13,
	})
	if err != nil {
		t.Fatalf("shadow: %v", err)
	}

	written := map[string]bool{}
	for _, file := range result.WrittenFiles {
		written[filepath.Base(filepath.Dir(file))+"/"+filepath.Base(file)] = true
	}
	if !written["RoleAuditTrigger/V0013.schema.json"] || !written["RoleChange/V0013.schema.json"] || len(written) != 2 {
		t.Fatalf("only V0013's two objects are regenerated, got %v", result.WrittenFiles)
	}
	for _, relPath := range []string{
		filepath.Join("Trigger", "RoleAuditTrigger", "V0013.schema.json"),
		filepath.Join("Table", "RoleChange", "V0013.schema.json"),
	} {
		want := readNormalized(t, filepath.Join(committedSampleMigrations, relPath))
		got := readNormalized(t, filepath.Join(outDir, relPath))
		if want != got {
			t.Fatalf("%s: regenerated snapshot differs from committed (modulo generatedAt)\nwant: %s\ngot:  %s", relPath, want, got)
		}
	}
}

func TestShadow_TheTrustedBaselineRestoresTriggersFromTheirSnapshots(t *testing.T) {
	registry, err := samplemigrations.App()
	if err != nil {
		t.Fatal(err)
	}
	result, err := sqlite.Shadow(context.Background(), sqlite.ShadowOptions{
		Set: registry.Migrations, Entities: registry.Entities, Triggers: registry.Triggers, OutDir: seededOutDir(t), From: 13,
	})
	if err != nil {
		t.Fatalf("shadow: %v", err)
	}
	restored := false
	for _, note := range result.Notes {
		if strings.Contains(note, "trg_roles_audit restored from V0013 snapshot (trusted)") {
			restored = true
		}
	}
	if !restored {
		t.Errorf("expected the trigger restored from its snapshot, got notes %v", result.Notes)
	}
	if len(result.WrittenFiles) != 0 {
		t.Errorf("nothing after the trusted version: %v", result.WrittenFiles)
	}
}
