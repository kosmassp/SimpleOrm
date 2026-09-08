package migrations

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
)

// diffChangedTable is one table-backed entity's diff, carried from the scan
// phase to the file-planning phase.
type diffChangedTable struct {
	Type reflect.Type
	Map  *core.EntityMap
	Diff *TableDiff
}

// diffChangedView is one view-backed entity's DDL change.
type diffChangedView struct {
	Type        reflect.Type
	Folder      string
	ObjectName  string
	DDL         string
	PreviousDDL *string
}

// diffPlannedFile is one file ExecuteDiff would write, computed before anything is touched.
type diffPlannedFile struct {
	Path    string
	Content string
}

// DiffOptions are the inputs of `simpleorm diff` (ADR-0017), the amend mode
// included (add.3) — the Go analog of the reference's DiffOptions
// (CODING-STANDARD §10): C# scans one assembly for both the compiled
// migration classes and the file layout; Go has no such scan, so Set carries
// the compiled versions and OutDir/Package carry the file layout separately.
type DiffOptions struct {
	// Set is the compiled migration versions (nil or empty means no versions yet).
	Set *Set
	// EntityTypes are the entities to diff — every mapped type, for the CLI.
	EntityTypes []reflect.Type
	// OutDir is the migrations directory: snapshots are read from it, sources are written into it.
	OutDir string
	// Package is the migrations package's import path; the emitted root imports
	// step packages under <Package>/Table/<Type> and its own package name is
	// Package's last path element.
	Package string
	Dialect core.Dialect
	// DialectLabel is the CLI's --dialect name, stamped into the generated root.
	DialectLabel string
	// Name is the step description (file and type name suffix); "Auto" when empty.
	Name string
	// Renames are declared column renames per relation name — never inferred (ADR-0017).
	Renames map[string]map[string]string
	// AllowRemove: destructive changes (DDL-003) are emitted only when set.
	AllowRemove bool
	// Amend regenerates the newest version in place instead of emitting the next one (ADR-0017 add.3).
	Amend bool
	// Force, amend only: replace a hand-written version too (its raw SQL, hooks,
	// data steps, and Down() overrides are not reproducible from the diff — the
	// run names every such file so they can be re-added by hand).
	Force bool
	// IsApplied, amend only, when a database was given: whether it has the
	// version recorded as applied. The answer only ever warns.
	IsApplied func(ctx context.Context, version int64) (bool, error)
}

var diffKindFolders = []string{"Table", "View", "MaterializedView"}

// ExecuteDiff is `simpleorm diff`: the model is the final truth, the
// committed snapshots are the recorded past, the difference is the next
// migration — ordinary source with literal SQL, no database needed
// (ADR-0017). Tables diff by columns, views by normalized DDL; new tables
// order FK-referenced first; views compose after tables (§7.22). The recorded
// past must exist: an object earlier versions migrate but no snapshot
// describes would diff as brand new, so it refuses and points at shadow/snapshot.
//
// Amend (ADR-0017 add.3) regenerates the newest version instead: the model
// against the schema the migrations below it produce (the snapshot history
// below it — a snapshot of the draft would otherwise hide the change),
// replacing the version's files. A version without the generator's header is
// hand-written — its raw SQL, hooks, and data steps are not reproducible from
// a diff — so replacing it takes --force and names every such file. Nothing
// on disk changes until every refusal has passed: the plan is fully computed
// before the first write.
func ExecuteDiff(ctx context.Context, options DiffOptions, stdout, stderr io.Writer) int {
	fail := func(message string) int {
		fmt.Fprintln(stderr, message)
		return 1
	}

	// 1. The target version: the next one, or -- amending -- the newest one.
	latest := int64(0)
	if options.Set != nil {
		for _, n := range options.Set.VersionNumbers() {
			if n > latest {
				latest = n
			}
		}
	}
	if options.Amend && latest == 0 {
		return fail(fmt.Sprintf("nothing to amend: no migration versions in %s", options.Package))
	}
	version := latest + 1
	if options.Amend {
		version = latest
	}
	prefix := fmt.Sprintf("V%04d", version)

	// 2-3. Amend: locate the version's files and vet them.
	var replaceable []string
	var notices []string
	if options.Amend {
		if refusal := locateVersionFiles(options, version, prefix, &replaceable, &notices); refusal != "" {
			return fail(refusal)
		}
	}

	// 4-5. The diff, against the schema the migrations below the target
	// version produce -- the snapshot history below it.
	migratedBelow := entitiesMigratedBelow(options.Set, version)
	loader := metadata.NewLoader(nil)

	var changed []diffChangedTable
	var viewChanges []diffChangedView
	var problems, removals, unrecorded []string

	types := append([]reflect.Type(nil), options.EntityTypes...)
	sort.Slice(types, func(i, j int) bool { return types[i].Name() < types[j].Name() })

	for _, t := range types {
		m, err := loader.Load(t)
		if err != nil {
			return fail(err.Error())
		}

		switch {
		case m.Kind == core.RelationTable:
			snapshotDir := filepath.Join(options.OutDir, "Table", t.Name())
			baseline := latestTableSnapshotBelow(snapshotDir, version)
			if baseline == nil && migratedBelow[t] {
				unrecorded = append(unrecorded, m.RelationName)
				continue
			}

			renames := lookupRenames(options.Renames, m.RelationName)
			diff := Diff(m, options.Dialect, baseline, renames)
			for _, message := range diff.Unsupported {
				problems = append(problems, m.RelationName+": "+message)
			}
			for _, column := range diff.Removed {
				removals = append(removals, m.RelationName+"."+column.Name)
			}
			for _, name := range diff.RemovedIndexNames {
				removals = append(removals, "index "+name)
			}
			if diff.HasChanges() {
				changed = append(changed, diffChangedTable{Type: t, Map: m, Diff: diff})
			}

		case m.Kind == core.RelationView || (m.Kind == core.RelationMaterializedView && options.Dialect.SupportsMaterializedViews()):
			folder := "View"
			if m.Kind == core.RelationMaterializedView {
				folder = "MaterializedView"
			}
			current := NormalizeDDL(options.Dialect.CreateViewSQL(m))
			snapshotDir := filepath.Join(options.OutDir, folder, t.Name())
			previous := latestDDLSnapshotBelow(snapshotDir, version)
			if previous == nil && migratedBelow[t] {
				unrecorded = append(unrecorded, m.RelationName)
				continue
			}
			if previous == nil || *previous != current {
				viewChanges = append(viewChanges, diffChangedView{
					Type: t, Folder: folder, ObjectName: m.RelationName, DDL: current, PreviousDDL: previous,
				})
			}
		}
	}

	if len(unrecorded) > 0 {
		return fail(fmt.Sprintf(
			"no recorded schema below %s for %s — earlier versions migrate them but no snapshot describes them, "+
				"so the diff would create them anew; run simpleorm shadow --to V%04d (sqlite) or snapshot on a "+
				"database migrated to V%04d, then retry",
			prefix, strings.Join(unrecorded, ", "), version-1, version-1))
	}

	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(stderr, "DDL-004 "+problem)
		}
		return 1
	}

	if len(removals) > 0 && !options.AllowRemove {
		return fail("DDL-003 destructive changes need --allow-remove: " + strings.Join(removals, ", "))
	}

	// The files this run would write, computed before anything is touched.
	description := options.Name
	if description == "" {
		description = "Auto"
	}

	var planned []diffPlannedFile
	var stepRefs []RootStepRef

	if len(changed) > 0 || len(viewChanges) > 0 {
		newSet := map[reflect.Type]bool{}
		var newOnes, modified []diffChangedTable
		for _, c := range changed {
			if c.Diff.IsNew {
				newSet[c.Type] = true
				newOnes = append(newOnes, c)
			} else {
				modified = append(modified, c)
			}
		}
		ordered := topologicalByForeignKey(newOnes, newSet)
		ordered = append(ordered, modified...)

		for _, entry := range ordered {
			typeName := entry.Type.Name()
			className := prefix + "_" + description
			content, err := EmitTableStep(entry.Map, options.Dialect, version, description, entry.Diff)
			if err != nil {
				return fail(err.Error())
			}
			planned = append(planned, diffPlannedFile{
				Path:    filepath.Join(options.OutDir, "Table", typeName, className+".go"),
				Content: content,
			})
			stepRefs = append(stepRefs, RootStepRef{
				ImportPath: options.Package + "/Table/" + typeName,
				Package:    strings.ToLower(typeName),
				TypeName:   className,
			})
		}

		for _, entry := range viewChanges {
			typeName := entry.Type.Name()
			className := prefix + "_" + description
			content, err := EmitViewStep(
				entry.Type, entry.Folder == "MaterializedView", entry.ObjectName, version, description, entry.DDL, entry.PreviousDDL)
			if err != nil {
				return fail(err.Error())
			}
			planned = append(planned, diffPlannedFile{
				Path:    filepath.Join(options.OutDir, entry.Folder, typeName, className+".go"),
				Content: content,
			})
			stepRefs = append(stepRefs, RootStepRef{
				ImportPath: options.Package + "/" + entry.Folder + "/" + typeName,
				Package:    strings.ToLower(typeName),
				TypeName:   className,
			})
		}

		rootContent, err := EmitRoot(options.Package, version, stepRefs, options.DialectLabel)
		if err != nil {
			return fail(err.Error())
		}
		planned = append(planned, diffPlannedFile{Path: filepath.Join(options.OutDir, prefix+".go"), Content: rootContent})
	}

	// A file that exists and is not the tool's own for this version is never overwritten.
	for _, file := range planned {
		if _, err := os.Stat(file.Path); err == nil && !containsPath(replaceable, file.Path) {
			return fail("refusing to overwrite " + file.Path)
		}
	}

	if len(planned) == 0 && !options.Amend {
		fmt.Fprintln(stdout, "no schema changes: the model matches the snapshots")
		return 0
	}

	if options.Amend && options.IsApplied != nil {
		applied, err := options.IsApplied(ctx, version)
		if err != nil {
			return fail(err.Error())
		}
		if applied {
			fmt.Fprintf(stdout,
				"warning: %s is recorded as applied in the database; amending changes its checksum, so migrate "+
					"reports MIG-010 there — migrate down --to %d or recreate that database before re-applying\n",
				prefix, version-1)
		}
	}

	// 6. Commit: every refusal has passed; the tree changes here and only here.
	for _, notice := range notices {
		fmt.Fprintln(stdout, notice)
	}

	var plannedPaths []string
	for _, file := range planned {
		plannedPaths = append(plannedPaths, file.Path)
	}
	for _, path := range replaceable {
		if containsPath(plannedPaths, path) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fail(err.Error())
		}
		fmt.Fprintln(stdout, "deleted "+path)
	}

	for _, file := range planned {
		if err := os.MkdirAll(filepath.Dir(file.Path), 0o777); err != nil {
			return fail(err.Error())
		}
		if err := os.WriteFile(file.Path, []byte(file.Content), 0o666); err != nil {
			return fail(err.Error())
		}
		fmt.Fprintln(stdout, "wrote "+file.Path)
	}

	if len(planned) == 0 {
		fmt.Fprintf(stdout, "%s removed: the model matches the snapshot history below it, so the version has no content\n", prefix)
		return 0
	}

	fmt.Fprintf(stdout, "review the generated %s, build, migrate, then refresh snapshots: simpleorm snapshot --out %s\n",
		prefix, options.OutDir)
	return 0
}

// locateVersionFiles is amend steps 2-3: the root, every V000N_*.go step under
// the generator's layout, and any snapshot stamped at that version (it
// describes the draft). Refuses -- naming the file -- when the root or a step
// lacks the generator's header and --force was not given, when the root was
// stamped for another dialect, or when the number of step files disagrees
// with the number of steps the compiled version composes: the layout
// diverged from the tool's, and the tool does not guess.
func locateVersionFiles(options DiffOptions, version int64, prefix string, replaceable, notices *[]string) string {
	rootFile := filepath.Join(options.OutDir, prefix+".go")
	rootBytes, err := os.ReadFile(rootFile)
	if err != nil {
		return fmt.Sprintf("amend: %s not found — %s is the newest version but has no root under --out", rootFile, prefix)
	}
	rootSource := string(rootBytes)

	stamped := GeneratedDialectLabel(rootSource)
	if stamped != "" && !strings.EqualFold(stamped, options.DialectLabel) {
		return fmt.Sprintf(
			"amend refuses: %s was generated for dialect %s; storage types are dialect-specific — run with --dialect %s",
			prefix, stamped, stamped)
	}

	var objectDirs []string
	for _, kind := range diffKindFolders {
		kindDir := filepath.Join(options.OutDir, kind)
		entries, err := os.ReadDir(kindDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				objectDirs = append(objectDirs, filepath.Join(kindDir, entry.Name()))
			}
		}
	}

	var stepFiles []string
	for _, dir := range objectDirs {
		matches, _ := filepath.Glob(filepath.Join(dir, prefix+"_*.go"))
		stepFiles = append(stepFiles, matches...)
	}
	sort.Strings(stepFiles)

	files := append([]string{rootFile}, stepFiles...)
	for _, file := range files {
		content := rootSource
		if file != rootFile {
			data, err := os.ReadFile(file)
			if err != nil {
				return err.Error()
			}
			content = string(data)
		}
		if IsGenerated(content) {
			continue
		}
		if !options.Force {
			return fmt.Sprintf(
				"amend refuses: %s is hand-written (no '%s' header) — its raw SQL, hooks, data steps, and Down() "+
					"are not reproducible from the diff; add --force to replace it anyway and re-add those by hand",
				file, GeneratedMarker)
		}
		*notices = append(*notices, fmt.Sprintf(
			"replacing hand-written %s (--force): re-add by hand any raw SQL, hooks, data steps, or Down() it carried that still apply", file))
	}

	compiledSteps := 0
	if v, ok := versionByNumber(options.Set, version); ok {
		builder := &VersionBuilder{}
		v.Compose(builder)
		compiledSteps = len(builder.Steps())
	}
	if compiledSteps != len(stepFiles) {
		return fmt.Sprintf(
			"amend refuses: %s compiles %d step(s) but %d %s_*.go file(s) exist under %s — the layout diverged from the generator's",
			prefix, compiledSteps, len(stepFiles), prefix, options.OutDir)
	}

	*replaceable = append(*replaceable, rootFile)
	*replaceable = append(*replaceable, stepFiles...)
	for _, dir := range objectDirs {
		matches, _ := filepath.Glob(filepath.Join(dir, prefix+".schema.json"))
		*replaceable = append(*replaceable, matches...)
	}
	return ""
}

// versionByNumber finds the Version in set whose parsed number is number.
func versionByNumber(set *Set, number int64) (Version, bool) {
	if set == nil {
		return nil, false
	}
	versions := set.Versions()
	numbers := set.VersionNumbers()
	for i, n := range numbers {
		if n == number {
			return versions[i], true
		}
	}
	return nil, false
}

// entitiesMigratedBelow is the entity types that migration steps below
// version touch (table/view steps in set's composed versions): objects with a
// migration history, which therefore need a recorded schema to diff against.
func entitiesMigratedBelow(set *Set, version int64) map[reflect.Type]bool {
	migrated := map[reflect.Type]bool{}
	if set == nil {
		return migrated
	}
	versions := set.Versions()
	numbers := set.VersionNumbers()
	for i, v := range versions {
		if numbers[i] >= version {
			continue
		}
		builder := &VersionBuilder{}
		v.Compose(builder)
		for _, step := range builder.Steps() {
			desc := step.describe()
			if desc.kind == stepKindTable || desc.kind == stepKindView {
				migrated[desc.entityType] = true
			}
		}
	}
	return migrated
}

// latestTableSnapshotBelow is the object's latest table snapshot strictly
// below version, read from dir's V*.schema.json files, or nil (a new table,
// or unrecorded history).
func latestTableSnapshotBelow(dir string, version int64) *TableSchema {
	schema, _, ok := latestTableSnapshot(dir, func(v int64) bool { return v < version })
	if !ok {
		return nil
	}
	return schema
}

// latestDDLSnapshotBelow is the object's latest view/materialized-view DDL
// snapshot strictly below version, or nil.
func latestDDLSnapshotBelow(dir string, version int64) *string {
	_, ddl, _, ok := latestDDLSnapshot(dir, func(v int64) bool { return v < version })
	if !ok {
		return nil
	}
	return &ddl
}

// lookupRenames finds relation's declared renames case-insensitively; nil is treated as none.
func lookupRenames(renames map[string]map[string]string, relation string) map[string]string {
	for name, columns := range renames {
		if strings.EqualFold(name, relation) {
			return columns
		}
	}
	return nil
}

func containsPath(haystack []string, path string) bool {
	for _, candidate := range haystack {
		if strings.EqualFold(filepath.Clean(candidate), filepath.Clean(path)) {
			return true
		}
	}
	return false
}

// topologicalByForeignKey orders newTables so an FK-referenced new table
// (within newSet) is emitted before the table referencing it.
func topologicalByForeignKey(newTables []diffChangedTable, newSet map[reflect.Type]bool) []diffChangedTable {
	var ordered []diffChangedTable
	visited := map[reflect.Type]bool{}

	var visit func(node diffChangedTable)
	visit = func(node diffChangedTable) {
		if visited[node.Type] {
			return
		}
		visited[node.Type] = true

		for _, property := range node.Map.Properties {
			if property.ForeignKeyReferences == nil || !newSet[property.ForeignKeyReferences] {
				continue
			}
			for _, candidate := range newTables {
				if candidate.Type == property.ForeignKeyReferences {
					visit(candidate)
					break
				}
			}
		}
		ordered = append(ordered, node)
	}

	for _, node := range newTables {
		visit(node)
	}
	return ordered
}
