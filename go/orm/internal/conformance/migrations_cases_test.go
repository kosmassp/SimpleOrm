package conformance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// migrationsCase is conformance/migrations-cases/*.json (spec/migrations.md):
// a migration set as data plus commands and expectations, run against a
// fresh database. Mirrors ConformanceMigrationTests.cs.
type migrationsCase struct {
	Name      string            `json:"name"`
	Versions  []migVersionSpec  `json:"versions"`
	Snapshots []json.RawMessage `json:"snapshots"`
	Run       []migRunStepSpec  `json:"run"`
}

type migVersionSpec struct {
	Version int64         `json:"version"`
	Steps   []migStepSpec `json:"steps"`
}

type migStepSpec struct {
	Object           string          `json:"object"`
	Description      string          `json:"description"`
	Up               []string        `json:"up"`
	Down             []string        `json:"down"`
	Renames          []migRenameSpec `json:"renames"`
	ExpectDefinition *string         `json:"expectDefinition"`
}

type migRenameSpec struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type migRunStepSpec struct {
	Command    string                     `json:"command"`
	Versions   []migVersionSpec           `json:"versions"`
	Force      bool                       `json:"force"`
	To         *int64                     `json:"to"`
	Version    *int64                     `json:"version"`
	Statements []string                   `json:"statements"`
	Expect     map[string]json.RawMessage `json:"expect"`
}

func TestMigrationsCases_BehaveAsSpecified(t *testing.T) {
	for _, file := range testsupport.ConformanceCases(t, "migrations-cases") {
		t.Run(file, func(t *testing.T) {
			data := testsupport.ReadConformance(t, "migrations-cases", file)
			var spec migrationsCase
			if err := json.Unmarshal(data, &spec); err != nil {
				t.Fatalf("parse case: %v", err)
			}

			ctx := context.Background()
			pool, err := sqlite.New().CreateConnection("Data Source=" + testsupport.TempDatabase(t))
			if err != nil {
				t.Fatalf("open pool: %v", err)
			}
			defer pool.Close()

			snapshots := loadCaseSnapshots(t, spec.Snapshots)
			defaultVersions := buildVersions(spec.Versions)
			maps := metadata.NewLoader(nil)
			dialect := sqlite.New()

			for _, step := range spec.Run {
				versions := defaultVersions
				if step.Versions != nil {
					versions = buildVersions(step.Versions)
				}
				set, err := migrations.NewSet(versions...)
				if err != nil {
					t.Fatalf("build set: %v", err)
				}
				runner := migrations.NewRunner(pool, dialect, maps, set, snapshots)

				var runErr error
				switch step.Command {
				case "migrate":
					_, runErr = runner.Migrate(ctx, migrations.RunOptions{AllowViewDrift: step.Force})
				case "down":
					if step.To == nil {
						t.Fatal("down command needs 'to'")
					}
					_, runErr = runner.MigrateDown(ctx, *step.To, migrations.RunOptions{AllowViewDrift: step.Force})
				case "baseline":
					if step.Version == nil {
						t.Fatal("baseline command needs 'version'")
					}
					runErr = runner.Baseline(ctx, *step.Version)
				case "sql":
					// The outside hotfix: statements applied without touching migration history.
					conn, connErr := pool.Conn(ctx)
					if connErr != nil {
						t.Fatalf("conn: %v", connErr)
					}
					runErr = migrations.ApplySync(ctx, conn, step.Statements)
					conn.Close()
				default:
					t.Fatalf("unknown command %q", step.Command)
				}

				if raw, ok := step.Expect["error"]; ok {
					var wantCode string
					if err := json.Unmarshal(raw, &wantCode); err != nil {
						t.Fatalf("parse expected error: %v", err)
					}
					if got := core.CodeOf(runErr); got != wantCode {
						t.Fatalf("%s: expected error %s, got %v", step.Command, wantCode, runErr)
					}
				} else {
					if runErr != nil {
						t.Fatalf("%s: unexpected error: %v", step.Command, runErr)
					}
					if raw, ok := step.Expect["applied"]; ok {
						assertApplied(t, ctx, pool, raw)
					}
				}

				if raw, ok := step.Expect["columns"]; ok {
					assertColumns(t, ctx, pool, raw)
				}
				if raw, ok := step.Expect["ddl"]; ok {
					assertDDL(t, ctx, pool, raw)
				}
			}
		})
	}
}

func buildVersions(specs []migVersionSpec) []migrations.Version {
	out := make([]migrations.Version, len(specs))
	for i, v := range specs {
		steps := make([]migrations.SQLVersionStep, len(v.Steps))
		for j, s := range v.Steps {
			var renames []migrations.ColumnRename
			for _, r := range s.Renames {
				renames = append(renames, migrations.ColumnRename{From: r.From, To: r.To})
			}
			expectDefinition := ""
			if s.ExpectDefinition != nil {
				expectDefinition = *s.ExpectDefinition
			}
			steps[j] = migrations.SQLVersionStep{
				ObjectName: s.Object, Description: s.Description, Up: s.Up, Down: s.Down,
				Renames: renames, ExpectDefinition: expectDefinition,
			}
		}
		out[i] = migrations.NewSQLVersion(v.Version, steps...)
	}
	return out
}

// loadCaseSnapshots writes the case's top-level snapshot documents to a temp
// directory (as sN.schema.json) and reads them back — the runner has no
// concept of "inline" snapshots, only files/embed.FS.
func loadCaseSnapshots(t *testing.T, raw []json.RawMessage) *migrations.SnapshotSet {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	dir := t.TempDir()
	for i, snapshot := range raw {
		path := filepath.Join(dir, "s"+strconv.Itoa(i)+".schema.json")
		if err := os.WriteFile(path, snapshot, 0o666); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
	}
	set, err := migrations.SnapshotsFromDirectory(dir)
	if err != nil {
		t.Fatalf("load snapshots: %v", err)
	}
	return set
}

type appliedPair struct {
	Version int64
	Object  string
}

func readAppliedRows(ctx context.Context, pool *sql.DB) ([]appliedPair, error) {
	rows, err := pool.QueryContext(ctx, "select version, object from schema_version")
	if err != nil {
		// No schema_version table yet: nothing recorded.
		return nil, nil
	}
	defer rows.Close()
	var out []appliedPair
	for rows.Next() {
		var p appliedPair
		if err := rows.Scan(&p.Version, &p.Object); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func assertApplied(t *testing.T, ctx context.Context, pool *sql.DB, raw json.RawMessage) {
	t.Helper()
	var expected [][2]any
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatalf("parse expected applied: %v", err)
	}
	want := make([]appliedPair, len(expected))
	for i, e := range expected {
		version, ok := e[0].(float64)
		if !ok {
			t.Fatalf("expected applied[%d][0] to be a number, got %T", i, e[0])
		}
		object, ok := e[1].(string)
		if !ok {
			t.Fatalf("expected applied[%d][1] to be a string, got %T", i, e[1])
		}
		want[i] = appliedPair{Version: int64(version), Object: object}
	}

	got, err := readAppliedRows(ctx, pool)
	if err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	sortApplied(want)
	sortApplied(got)
	if !equalApplied(want, got) {
		t.Fatalf("applied rows: want %+v, got %+v", want, got)
	}
}

func sortApplied(rows []appliedPair) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Version != rows[j].Version {
			return rows[i].Version < rows[j].Version
		}
		return rows[i].Object < rows[j].Object
	})
}

func equalApplied(a, b []appliedPair) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertColumns(t *testing.T, ctx context.Context, pool *sql.DB, raw json.RawMessage) {
	t.Helper()
	var expected map[string][]string
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatalf("parse expected columns: %v", err)
	}
	for table, want := range expected {
		rows, err := pool.QueryContext(ctx, "select name from pragma_table_info(@t) order by name", sql.Named("t", table))
		if err != nil {
			t.Fatalf("read columns for %s: %v", table, err)
		}
		var got []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatalf("scan column: %v", err)
			}
			got = append(got, name)
		}
		rows.Close()
		wantSorted := append([]string(nil), want...)
		sort.Strings(wantSorted)
		if !equalStrings(wantSorted, got) {
			t.Fatalf("columns of %s: want %v, got %v", table, wantSorted, got)
		}
	}
}

func assertDDL(t *testing.T, ctx context.Context, pool *sql.DB, raw json.RawMessage) {
	t.Helper()
	var expected map[string]string
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatalf("parse expected ddl: %v", err)
	}
	for view, want := range expected {
		var got string
		row := pool.QueryRowContext(ctx, "select sql from sqlite_master where type = 'view' and name = @v", sql.Named("v", view))
		if err := row.Scan(&got); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("view %s does not exist", view)
			}
			t.Fatalf("read ddl for %s: %v", view, err)
		}
		if migrations.NormalizeDDL(want) != migrations.NormalizeDDL(got) {
			t.Fatalf("ddl of %s: want %q, got %q", view, migrations.NormalizeDDL(want), migrations.NormalizeDDL(got))
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
