// Package cli is the simpleorm command as a library (§7.24, mirrors
// dotnet/src/SimpleOrm.Cli/Program.cs and php/src/Cli/Application.php): the
// application embeds it —
//
//	func main() { os.Exit(cli.Run(context.Background(), app.Registry, os.Args[1:], os.Stdout, os.Stderr)) }
//
// — instead of the reference's own executable, because a Go binary cannot
// load another binary's types the way the C# CLI loads an application
// assembly (CODING-STANDARD §10): migrations and entities are still code, but
// here they arrive as the caller's [orm.Registry] value, not a --assembly
// path. There is accordingly no --assembly or --namespace option; --package
// takes --namespace's place for `diff`, since generated Go source needs an
// import path rather than a .NET namespace.
package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// Run parses args (as the shell would pass argv[1:]) and executes one
// command against registry, writing to stdout/stderr and returning the
// process exit code: 0 success, 1 a command failure (its message printed to
// stderr), 2 no command or an unknown one (the usage printed to stdout).
// Every database command opens its own pool through the dialect and closes
// it — there is no shared session across commands (CODING-STANDARD §10).
func Run(ctx context.Context, registry orm.Registry, args []string, stdout, stderr io.Writer) int {
	parsed := parseArguments(args)
	if len(parsed.positional) == 0 {
		printUsage(stdout)
		return 2
	}

	command := strings.ToLower(parsed.positional[0])
	switch {
	case command == "migrate" && len(parsed.positional) > 1 && strings.ToLower(parsed.positional[1]) == "down":
		return runMigrateDown(ctx, registry, parsed, stdout, stderr)
	case command == "migrate":
		return runMigrate(ctx, registry, parsed, stdout, stderr)
	case command == "status":
		return runStatus(ctx, registry, parsed, stdout, stderr)
	case command == "baseline":
		return runBaseline(ctx, registry, parsed, stdout, stderr)
	case command == "export-metadata":
		return runExportMetadata(registry, parsed, stdout, stderr)
	case command == "validate":
		return runValidate(ctx, registry, parsed, stdout, stderr)
	case command == "snapshot":
		return runSnapshot(ctx, registry, parsed, stdout, stderr)
	case command == "diff":
		return runDiff(ctx, registry, parsed, stdout, stderr)
	case command == "shadow":
		return runShadow(ctx, registry, parsed, stdout, stderr)
	default:
		printUsage(stdout)
		return 2
	}
}

func fail(stderr io.Writer, message string) int {
	fmt.Fprintln(stderr, message)
	return 1
}

// --- argument parsing (mirrors Program.cs's inline parser / Arguments.php) --

// booleanFlags take no value; every other --name consumes the next token.
var booleanFlags = map[string]bool{"force": true, "allow-delete": true, "allow-remove": true, "amend": true}

type arguments struct {
	positional []string
	options    map[string][]string
}

func parseArguments(args []string) arguments {
	parsed := arguments{options: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		token := args[i]
		if !strings.HasPrefix(token, "--") {
			parsed.positional = append(parsed.positional, token)
			continue
		}
		name := strings.ToLower(token[2:])
		if _, ok := parsed.options[name]; !ok {
			parsed.options[name] = nil
		}
		if !booleanFlags[name] && i+1 < len(args) {
			i++
			parsed.options[name] = append(parsed.options[name], args[i])
		}
	}
	return parsed
}

// option is the last --name value given, matching Program.cs's Option().
func (a arguments) option(name string) (string, bool) {
	values := a.options[strings.ToLower(name)]
	if len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
}

// all returns every --name value given, in order (repeatable options such as --rename).
func (a arguments) all(name string) []string { return a.options[strings.ToLower(name)] }

func (a arguments) flag(name string) bool {
	_, ok := a.options[strings.ToLower(name)]
	return ok
}

// --- shared plumbing ---------------------------------------------------------

// dialectFor is the --dialect rule: sqlite is the only dialect this port
// supports (ADR-0027) — a Go analog would need SimpleOrm.SqlServer/Postgres
// ports this repository does not have yet; any other name refuses loudly
// rather than silently defaulting.
func dialectFor(a arguments) (orm.Dialect, string, error) {
	name := "sqlite"
	if value, ok := a.option("dialect"); ok {
		name = strings.ToLower(value)
	}
	if name != "sqlite" {
		return nil, "", fmt.Errorf("unknown --dialect '%s' (ADR-0027: this port supports only sqlite)", name)
	}
	return sqlite.New(), name, nil
}

func connectionString(a arguments) (string, error) {
	value, ok := a.option("db")
	if !ok {
		return "", fmt.Errorf("--db <connection string or file> is required")
	}
	if strings.Contains(value, "=") {
		return value, nil
	}
	return "Data Source=" + value, nil
}

func openPool(dialect orm.Dialect, a arguments) (*sql.DB, error) {
	connStr, err := connectionString(a)
	if err != nil {
		return nil, err
	}
	return dialect.CreateConnection(connStr)
}

// snapshotsFor resolves the rollback/history source (ADR-0018): --snapshots
// <dir> when given, else the registry's own embedded FS, else none.
func snapshotsFor(registry orm.Registry, a arguments) (*orm.SnapshotSet, error) {
	if dir, ok := a.option("snapshots"); ok {
		return orm.SnapshotsFromDirectory(dir)
	}
	if registry.Snapshots != nil {
		return orm.SnapshotsFromFS(registry.Snapshots)
	}
	return nil, nil
}

// openRunner opens a fresh pool and a runner over registry.Migrations; the
// caller closes the pool. Every migration-shaped command needs a declared
// migration set — an empty registry is a caller mistake, reported plainly.
func openRunner(dialect orm.Dialect, registry orm.Registry, a arguments) (*sql.DB, *migrations.Runner, error) {
	if registry.Migrations == nil {
		return nil, nil, fmt.Errorf("the registry declares no migrations")
	}
	pool, err := openPool(dialect, a)
	if err != nil {
		return nil, nil, err
	}
	snapshots, err := snapshotsFor(registry, a)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	maps := metadata.NewLoader(nil)
	return pool, migrations.NewRunner(pool, dialect, maps, registry.Migrations, snapshots), nil
}

func parseVersionArgument(raw string) (int64, error) {
	trimmed := raw
	if len(trimmed) > 0 && (trimmed[0] == 'V' || trimmed[0] == 'v') {
		trimmed = trimmed[1:]
	}
	return strconv.ParseInt(trimmed, 10, 64)
}

// --- migrate / migrate down --------------------------------------------------

func runMigrate(ctx context.Context, registry orm.Registry, a arguments, stdout, stderr io.Writer) int {
	dialect, _, err := dialectFor(a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	pool, runner, err := openRunner(dialect, registry, a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	defer pool.Close()

	force := a.flag("force")
	applied, err := runner.Migrate(ctx, migrations.RunOptions{
		AllowViewDrift: force,
		Notify:         func(message string) { fmt.Fprintln(stdout, "MIG-012 "+message) },
	})
	if err != nil {
		return fail(stderr, err.Error())
	}
	if applied == 0 {
		fmt.Fprintln(stdout, "nothing pending")
	} else {
		fmt.Fprintf(stdout, "applied %d version(s)\n", applied)
	}

	if !force {
		return 0
	}
	return runForceSync(ctx, pool, dialect, registry, a, stdout, stderr)
}

// runForceSync is `migrate --force`'s live-schema sync step (§7.23):
// additive fixes always apply, deletions need --allow-delete (DDL-003), and
// anything the sync cannot express safely is reported (DDL-004) with a
// non-zero exit.
func runForceSync(
	ctx context.Context, pool *sql.DB, dialect orm.Dialect, registry orm.Registry, a arguments, stdout, stderr io.Writer,
) int {
	conn, err := pool.Conn(ctx)
	if err != nil {
		return fail(stderr, err.Error())
	}
	defer conn.Close()

	maps := metadata.NewLoader(nil)
	plan, err := migrations.PlanSync(ctx, conn, dialect, maps, registry.Entities)
	if err != nil {
		return fail(stderr, err.Error())
	}

	if err := migrations.ApplySync(ctx, conn, plan.Additive); err != nil {
		return fail(stderr, err.Error())
	}
	for _, statement := range plan.Additive {
		fmt.Fprintln(stdout, "sync: "+statement)
	}

	if len(plan.Deletions) > 0 {
		if a.flag("allow-delete") {
			if err := migrations.ApplySync(ctx, conn, plan.Deletions); err != nil {
				return fail(stderr, err.Error())
			}
			for _, statement := range plan.Deletions {
				fmt.Fprintln(stdout, "sync (destructive): "+statement)
			}
		} else {
			for _, statement := range plan.Deletions {
				fmt.Fprintln(stdout, "DDL-003 skipped (needs --allow-delete): "+statement)
			}
		}
	}

	for _, message := range plan.Unsupported {
		fmt.Fprintln(stderr, "DDL-004 "+message)
	}
	if plan.IsEmpty() {
		fmt.Fprintln(stdout, "schema already matches the model")
	}
	if len(plan.Unsupported) > 0 {
		return 1
	}
	return 0
}

func runMigrateDown(ctx context.Context, registry orm.Registry, a arguments, stdout, stderr io.Writer) int {
	toRaw, ok := a.option("to")
	if !ok {
		return fail(stderr, "migrate down requires --to <version>")
	}
	target, err := strconv.ParseInt(toRaw, 10, 64)
	if err != nil {
		return fail(stderr, fmt.Sprintf("migrate down --to '%s': expected a version number", toRaw))
	}

	dialect, _, err := dialectFor(a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	pool, runner, err := openRunner(dialect, registry, a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	defer pool.Close()

	reverted, err := runner.MigrateDown(ctx, target, migrations.RunOptions{
		AllowViewDrift: a.flag("force"),
		Notify:         func(message string) { fmt.Fprintln(stdout, "MIG-012 "+message) },
	})
	if err != nil {
		return fail(stderr, err.Error())
	}
	fmt.Fprintf(stdout, "reverted %d version(s); now at <= V%04d\n", reverted, target)
	return 0
}

// --- status / baseline --------------------------------------------------------

func runStatus(ctx context.Context, registry orm.Registry, a arguments, stdout, stderr io.Writer) int {
	dialect, _, err := dialectFor(a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	pool, runner, err := openRunner(dialect, registry, a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	defer pool.Close()

	entries, err := runner.Status(ctx)
	if err != nil {
		return fail(stderr, err.Error())
	}
	for _, entry := range entries {
		fmt.Fprintln(stdout, entry.String())
	}
	return 0
}

func runBaseline(ctx context.Context, registry orm.Registry, a arguments, stdout, stderr io.Writer) int {
	raw, ok := a.option("version")
	if !ok {
		return fail(stderr, "baseline requires --version <N>")
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fail(stderr, fmt.Sprintf("baseline --version '%s': expected a version number", raw))
	}

	dialect, _, err := dialectFor(a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	pool, runner, err := openRunner(dialect, registry, a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	defer pool.Close()

	if err := runner.Baseline(ctx, version); err != nil {
		return fail(stderr, err.Error())
	}
	fmt.Fprintf(stdout, "baselined at V%04d\n", version)
	return 0
}

// --- export-metadata / validate -----------------------------------------------

func runExportMetadata(registry orm.Registry, a arguments, stdout, stderr io.Writer) int {
	outDir, hasOut := a.option("out")
	if hasOut {
		if err := os.MkdirAll(outDir, 0o777); err != nil {
			return fail(stderr, err.Error())
		}
	}

	maps := metadata.NewLoader(nil)
	for _, entityType := range registry.Entities {
		m, err := maps.Load(entityType)
		if err != nil {
			return fail(stderr, err.Error())
		}
		document, err := orm.ExportEntityMap(m, maps)
		if err != nil {
			return fail(stderr, err.Error())
		}
		if !hasOut {
			fmt.Fprintln(stdout, document)
			continue
		}
		file := filepath.Join(outDir, orm.SnakeCase{}.TableName(entityType.Name())+".json")
		if err := os.WriteFile(file, []byte(document+"\n"), 0o666); err != nil {
			return fail(stderr, err.Error())
		}
		fmt.Fprintln(stdout, "wrote "+file)
	}
	return 0
}

func runValidate(ctx context.Context, registry orm.Registry, a arguments, stdout, stderr io.Writer) int {
	dialect, _, err := dialectFor(a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	connStr, err := connectionString(a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	db, err := orm.Open(ctx, connStr, orm.Options{Dialect: dialect})
	if err != nil {
		return fail(stderr, err.Error())
	}
	defer db.Close()

	if err := orm.Validate(ctx, db, registry); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "valid")
	return 0
}

// --- snapshot ------------------------------------------------------------------

func runSnapshot(ctx context.Context, registry orm.Registry, a arguments, stdout, stderr io.Writer) int {
	outDir, ok := a.option("out")
	if !ok {
		return fail(stderr, "snapshot requires --out <MigrationsDir>")
	}
	dialect, _, err := dialectFor(a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	pool, runner, err := openRunner(dialect, registry, a)
	if err != nil {
		return fail(stderr, err.Error())
	}
	defer pool.Close()

	entries, err := runner.Status(ctx)
	if err != nil {
		return fail(stderr, err.Error())
	}
	// The last version touching each object, from the code-side plan (any state).
	perObject := map[string]int64{}
	for _, entry := range entries {
		if version, ok := perObject[entry.ObjectName]; !ok || entry.Version > version {
			perObject[entry.ObjectName] = entry.Version
		}
	}

	maps := metadata.NewLoader(nil)
	generatedAt := time.Now().UTC()
	for _, entityType := range registry.Entities {
		m, err := maps.Load(entityType)
		if err != nil {
			return fail(stderr, err.Error())
		}
		version, ok := perObject[m.RelationName]
		if !ok {
			continue
		}

		var folder, content string
		switch m.Kind {
		case orm.RelationTable:
			folder = "Table"
			content = migrations.Export(m, dialect, version, generatedAt)
		case orm.RelationView:
			folder = "View"
			content = migrations.ExportDDL(m.RelationName, "view", dialect.CreateViewSQL(m), version, generatedAt)
		case orm.RelationMaterializedView:
			if !dialect.SupportsMaterializedViews() {
				continue // dormant on this dialect: no history to record
			}
			folder = "MaterializedView"
			content = migrations.ExportDDL(m.RelationName, "materialized_view", dialect.CreateViewSQL(m), version, generatedAt)
		default:
			continue
		}

		directory := filepath.Join(outDir, folder, entityType.Name())
		if err := os.MkdirAll(directory, 0o777); err != nil {
			return fail(stderr, err.Error())
		}
		file := filepath.Join(directory, fmt.Sprintf("V%04d.schema.json", version))
		if err := os.WriteFile(file, []byte(content+"\n"), 0o666); err != nil {
			return fail(stderr, err.Error())
		}
		fmt.Fprintln(stdout, "wrote "+file)
	}
	return 0
}

// --- diff ------------------------------------------------------------------------

func runDiff(ctx context.Context, registry orm.Registry, a arguments, stdout, stderr io.Writer) int {
	outDir, ok := a.option("out")
	if !ok {
		return fail(stderr, "diff requires --out <MigrationsDir>")
	}
	dialect, dialectLabel, err := dialectFor(a)
	if err != nil {
		return fail(stderr, err.Error())
	}

	pkg, hasPkg := a.option("package")
	if !hasPkg {
		if registry.Migrations == nil || len(registry.Migrations.Versions()) == 0 {
			return fail(stderr, "diff requires --package <import path> when the migration set is empty")
		}
		pkg = reflect.TypeOf(registry.Migrations.Versions()[0]).PkgPath()
	}

	renames, err := parseRenames(a.all("rename"))
	if err != nil {
		return fail(stderr, err.Error())
	}

	name, _ := a.option("name")

	var isApplied func(context.Context, int64) (bool, error)
	if a.flag("amend") {
		if _, hasDB := a.option("db"); hasDB {
			isApplied = func(ctx context.Context, version int64) (bool, error) {
				pool, runner, err := openRunner(dialect, registry, a)
				if err != nil {
					return false, err
				}
				defer pool.Close()
				entries, err := runner.Status(ctx)
				if err != nil {
					return false, err
				}
				for _, entry := range entries {
					if entry.Version == version && (entry.State == migrations.Applied || entry.State == migrations.Drifted) {
						return true, nil
					}
				}
				return false, nil
			}
		}
	}

	return migrations.ExecuteDiff(ctx, migrations.DiffOptions{
		Set:          registry.Migrations,
		EntityTypes:  registry.Entities,
		OutDir:       outDir,
		Package:      pkg,
		Dialect:      dialect,
		DialectLabel: dialectLabel,
		Name:         name,
		Renames:      renames,
		AllowRemove:  a.flag("allow-remove"),
		Amend:        a.flag("amend"),
		Force:        a.flag("force"),
		IsApplied:    isApplied,
	}, stdout, stderr)
}

// parseRenames parses repeatable --rename <table>.<oldColumn>=<newColumn> tokens.
func parseRenames(raw []string) (map[string]map[string]string, error) {
	renames := map[string]map[string]string{}
	for _, token := range raw {
		dot := strings.IndexByte(token, '.')
		eq := strings.IndexByte(token, '=')
		if dot <= 0 || eq <= dot+1 || eq == len(token)-1 {
			return nil, fmt.Errorf("--rename '%s': expected <table>.<oldColumn>=<newColumn>", token)
		}
		table := token[:dot]
		for existing := range renames {
			if strings.EqualFold(existing, table) {
				table = existing
				break
			}
		}
		if renames[table] == nil {
			renames[table] = map[string]string{}
		}
		renames[table][token[dot+1:eq]] = token[eq+1:]
	}
	return renames, nil
}

// --- shadow ------------------------------------------------------------------------

func runShadow(ctx context.Context, registry orm.Registry, a arguments, stdout, stderr io.Writer) int {
	if name, ok := a.option("dialect"); ok && !strings.EqualFold(name, "sqlite") {
		return fail(stderr, fmt.Sprintf(
			"shadow replays into a throwaway SQLite database and supports only --dialect sqlite (ADR-0027); "+
				"'%s' rollbacks need Down() overrides until snapshot tooling learns that dialect", name))
	}
	outDir, ok := a.option("out")
	if !ok {
		return fail(stderr, "shadow requires --out <MigrationsDir>")
	}

	var from, to int64
	if raw, ok := a.option("from"); ok {
		value, err := parseVersionArgument(raw)
		if err != nil {
			return fail(stderr, fmt.Sprintf("--from '%s': expected V000N or a number", raw))
		}
		from = value
	}
	if raw, ok := a.option("to"); ok {
		value, err := parseVersionArgument(raw)
		if err != nil {
			return fail(stderr, fmt.Sprintf("--to '%s': expected V000M or a number", raw))
		}
		to = value
	}

	if registry.Migrations == nil {
		return fail(stderr, "the registry declares no migrations")
	}

	result, err := sqlite.Shadow(ctx, sqlite.ShadowOptions{
		Set: registry.Migrations, Entities: registry.Entities, OutDir: outDir, From: from, To: to,
	})
	if err != nil {
		return fail(stderr, err.Error())
	}
	for _, note := range result.Notes {
		fmt.Fprintln(stdout, note)
	}
	for _, file := range result.WrittenFiles {
		fmt.Fprintln(stdout, "wrote "+file)
	}
	if len(result.WrittenFiles) == 0 {
		fmt.Fprintln(stdout, "nothing to regenerate")
	} else {
		fmt.Fprintf(stdout, "regenerated %d snapshot(s)\n", len(result.WrittenFiles))
	}
	return 0
}

// --- usage -------------------------------------------------------------------------

func printUsage(w io.Writer) { fmt.Fprint(w, usageText) }

const usageText = `simpleorm — SQL-first micro-ORM CLI

commands (migrate/status/baseline/validate/snapshot/shadow also need --db,
except shadow which only replays into its own throwaway database):
  migrate [--force [--allow-delete]]
                              apply pending versions; a guarded view step refuses
                              when the live view was changed outside the code
                              (MIG-012) unless --force recreates it; --force then
                              also syncs the live schema to the model (additive
                              only; deletions need --allow-delete, DDL-003)
  migrate down --to <N> [--snapshots <MigrationsDir>] [--force]
                              revert versions above N; rollbacks derive from the
                              snapshots (the registry's embedded FS, or --snapshots);
                              Down() overrides; no snapshot is MIG-020
  status                      list (version, object) states
  baseline --version <N>      record versions <= N without running them
  export-metadata [--out dir] write each entity's EntityMap JSON
  validate                    SchemaGuard: full report or exit 0
  snapshot --out <MigrationsDir>
                              write V000N.schema.json per object (tables by
                              columns; views by DDL), versioned and timestamped
  diff --out <MigrationsDir> [--package <import path>]
       [--name <Description>] [--rename table.old=new]... [--allow-remove]
       [--amend [--force] [--db <value>]]
                              generate the next migration version from the model vs
                              the latest snapshots (tables by columns, views by
                              DDL); no database needed; removals need
                              --allow-remove (DDL-003); inexpressible changes are
                              DDL-004 (write by hand); a migrated object with no
                              snapshot refuses (run shadow/snapshot first).
                              --amend regenerates the newest version in place
                              instead (baseline: the snapshots below it); a
                              hand-written version is replaced only with --force
                              (its raw SQL/hooks/data are not reproducible — re-add
                              by hand); with --db, an applied draft gets a MIG-010
                              heads-up (migrate down first, or recreate)
  shadow --out <MigrationsDir> [--from V000N] [--to V000M]
                              rebuild snapshots by replaying migrations in a
                              throwaway database; --from trusts version N as
                              correct (baseline from committed snapshots, no
                              verification below N) and regenerates only (N, M]

options:
  --db <value>        connection string, or a bare path to a SQLite file
  --dialect <name>    sqlite (default) — the only dialect this port supports
                      (ADR-0027); any other name refuses
  --snapshots <dir>   read schema snapshots from this directory instead of
                      the registry's embedded ones
  --package <path>    the migrations package's import path for generated code
                      (default: the first migration version's own package)
`
