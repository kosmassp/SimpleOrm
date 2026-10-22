package migrations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The snapshot shape of a table lives in core, where the dialect seam renders DDL from it (ADR-0043); this
// package produces, reads, and diffs it.
type (
	TableColumn = core.TableColumn
	IndexPart   = core.IndexPart
	TableIndex  = core.TableIndex
	TableSchema = core.TableSchema
)

// FromMap is the current model rendered as a schema (the metadata-side producer).
func FromMap(m *core.EntityMap, dialect core.Dialect) *TableSchema {
	columns := make([]TableColumn, len(m.Properties))
	for i, p := range m.Properties {
		columns[i] = TableColumn{
			Name: p.ColumnName, StorageType: dialect.StorageType(m, p), Nullable: p.IsNullable,
			Key: p.IsKey, Generated: p.IsGenerated,
		}
	}
	indexes := make([]TableIndex, len(m.Indexes))
	for i, index := range m.Indexes {
		parts := make([]IndexPart, len(index.Columns))
		for j, c := range index.Columns {
			parts[j] = IndexPart{ColumnName: c.ColumnName, Descending: c.Descending}
		}
		indexes[i] = TableIndex{Name: index.Name, Columns: parts, Unique: index.Unique}
	}
	return &TableSchema{Name: m.RelationName, Dialect: dialect.Name(), Columns: columns, Indexes: indexes}
}

// Export renders map's current shape as a table snapshot document (see ExportSchema), stamped for the dialect.
func Export(m *core.EntityMap, dialect core.Dialect, asOfVersion int64, generatedAt time.Time) string {
	return ExportSchema(FromMap(m, dialect), asOfVersion, generatedAt)
}

// ExportSchema is the table snapshot document for schema (ADR-0017, format v3 since ADR-0043): object, the
// dialect stamp when schema.Dialect names a dialect other than sqlite, asOfVersion, generatedAt, then columns
// and indexes name-sorted (ordinal byte order) for determinism — byte-identical across the implementations.
func ExportSchema(schema *TableSchema, asOfVersion int64, generatedAt time.Time) string {
	columns := append([]TableColumn(nil), schema.Columns...)
	sort.Slice(columns, func(i, j int) bool { return columns[i].Name < columns[j].Name })
	columnsJSON := make([]any, len(columns))
	for i, c := range columns {
		obj := core.JSONObject{}
		obj = obj.Set("column", c.Name)
		obj = obj.Set("type", c.StorageType)
		obj = obj.Set("nullable", c.Nullable)
		if c.Key {
			obj = obj.Set("key", true)
		}
		if c.Generated {
			obj = obj.Set("generated", true)
		}
		columnsJSON[i] = obj
	}

	indexes := append([]TableIndex(nil), schema.Indexes...)
	sort.Slice(indexes, func(i, j int) bool { return indexes[i].Name < indexes[j].Name })
	indexesJSON := make([]any, len(indexes))
	for i, index := range indexes {
		parts := make([]any, len(index.Columns))
		for j, part := range index.Columns {
			direction := "asc"
			if part.Descending {
				direction = "desc"
			}
			partObj := core.JSONObject{}
			partObj = partObj.Set("column", part.ColumnName)
			partObj = partObj.Set("direction", direction)
			parts[j] = partObj
		}
		obj := core.JSONObject{}
		obj = obj.Set("name", index.Name)
		obj = obj.Set("columns", parts)
		if index.Unique {
			obj = obj.Set("unique", true)
		}
		indexesJSON[i] = obj
	}

	doc := core.JSONObject{}
	doc = doc.Set("object", schema.Name)
	doc = withDialectStamp(doc, schema.Dialect)
	doc = doc.Set("asOfVersion", asOfVersion)
	doc = doc.Set("generatedAt", core.FormatUTC(generatedAt))
	doc = doc.Set("columns", columnsJSON)
	doc = doc.Set("indexes", indexesJSON)
	return core.CanonicalJSON(doc)
}

// ExportDDL is the DDL-shaped snapshot for view-, materialized-view-, and
// procedure-backed objects (ADR-0017 add.1): those self-reflect from their
// defining SQL, so their history is compared by (normalized) DDL, not columns.
func ExportDDL(objectName, dialectName, kind, ddl string, asOfVersion int64, generatedAt time.Time) string {
	doc := core.JSONObject{}
	doc = doc.Set("object", objectName)
	doc = withDialectStamp(doc, dialectName)
	doc = doc.Set("kind", kind)
	doc = doc.Set("asOfVersion", asOfVersion)
	doc = doc.Set("generatedAt", core.FormatUTC(generatedAt))
	doc = doc.Set("ddl", NormalizeDDL(ddl))
	return core.CanonicalJSON(doc)
}

// withDialectStamp writes the format-v3 stamp (ADR-0043) right after "object": every snapshot but SQLite's
// carries `"dialect": "<name>"`; an unstamped document is SQLite's, so nothing on disk changes for a SQLite tree.
func withDialectStamp(doc core.JSONObject, dialectName string) core.JSONObject {
	if stamp := normalizeStamp(dialectName); stamp != "sqlite" {
		doc = doc.Set("dialect", stamp)
	}
	return doc
}

// normalizeStamp is a stamp as a dialect name: an absent (empty) stamp is SQLite's.
func normalizeStamp(stamp string) string {
	stamp = strings.ToLower(strings.TrimSpace(stamp))
	if stamp == "" {
		return "sqlite"
	}
	return stamp
}

// CheckSnapshotDialect is the MIG-014 check every snapshot reader makes (ADR-0043, spec/migrations.md
// "Dialects"): a tree and its snapshots belong to one dialect, so a document stamped for another one — the
// derived rollback, the diff baseline, and the shadow's trusted baseline all read through here — refuses,
// naming the object and both dialects. An unstamped document is SQLite's.
func CheckSnapshotDialect(stamp string, dialect core.Dialect, object string) error {
	if normalizeStamp(stamp) == dialect.Name() {
		return nil
	}
	return core.Errorf("MIG-014", object,
		"the snapshot is stamped for dialect %s but the dialect in use is %s; a tree and its snapshots belong to one dialect — "+
			"rerun snapshot/shadow with --dialect %s, or point at the right tree",
		normalizeStamp(stamp), dialect.Name(), dialect.Name())
}

type tableSnapshotColumnJSON struct {
	Column    string `json:"column"`
	Type      string `json:"type"`
	Nullable  bool   `json:"nullable"`
	Key       bool   `json:"key"`
	Generated bool   `json:"generated"`
}

type tableSnapshotIndexJSON struct {
	Name    string `json:"name"`
	Columns []struct {
		Column    string `json:"column"`
		Direction string `json:"direction"`
	} `json:"columns"`
	Unique bool `json:"unique"`
}

type tableSnapshotJSON struct {
	Object      string                    `json:"object"`
	Dialect     string                    `json:"dialect"`
	AsOfVersion int64                     `json:"asOfVersion"`
	Columns     []tableSnapshotColumnJSON `json:"columns"`
	Indexes     []tableSnapshotIndexJSON  `json:"indexes"`
}

// ParseTable reads a table snapshot document (format v2 or v3); the schema carries the document's stamp.
func ParseTable(data []byte) (*TableSchema, int64, error) {
	var doc tableSnapshotJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, 0, fmt.Errorf("parse table snapshot: %w", err)
	}
	columns := make([]TableColumn, len(doc.Columns))
	for i, c := range doc.Columns {
		columns[i] = TableColumn{Name: c.Column, StorageType: c.Type, Nullable: c.Nullable, Key: c.Key, Generated: c.Generated}
	}
	indexes := make([]TableIndex, len(doc.Indexes))
	for i, index := range doc.Indexes {
		parts := make([]IndexPart, len(index.Columns))
		for j, part := range index.Columns {
			parts[j] = IndexPart{ColumnName: part.Column, Descending: part.Direction == "desc"}
		}
		indexes[i] = TableIndex{Name: index.Name, Columns: parts, Unique: index.Unique}
	}
	return &TableSchema{Name: doc.Object, Dialect: doc.Dialect, Columns: columns, Indexes: indexes}, doc.AsOfVersion, nil
}

// DDLSnapshot is a DDL-shaped snapshot document read back: a view's, materialized view's, or procedure's
// neutral definition, with the dialect stamp it was written under ("" = sqlite).
type DDLSnapshot struct {
	Object      string
	Dialect     string
	Kind        string
	DDL         string
	AsOfVersion int64
}

// ParseDDLSnapshot reads a DDL-shaped snapshot document.
func ParseDDLSnapshot(data []byte) (*DDLSnapshot, error) {
	var doc struct {
		Object      string `json:"object"`
		Dialect     string `json:"dialect"`
		Kind        string `json:"kind"`
		AsOfVersion int64  `json:"asOfVersion"`
		DDL         string `json:"ddl"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse ddl snapshot: %w", err)
	}
	return &DDLSnapshot{Object: doc.Object, Dialect: doc.Dialect, Kind: doc.Kind, DDL: doc.DDL, AsOfVersion: doc.AsOfVersion}, nil
}

// ParseDDL reads a DDL-shaped snapshot document (the stamp is dropped; see ParseDDLSnapshot).
func ParseDDL(data []byte) (object, kind, ddl string, asOfVersion int64, err error) {
	snapshot, err := ParseDDLSnapshot(data)
	if err != nil {
		return "", "", "", 0, err
	}
	return snapshot.Object, snapshot.Kind, snapshot.DDL, snapshot.AsOfVersion, nil
}

// viewDDLPattern is a view create in any dialect's prefix: `create [or alter|or replace] [materialized] view
// [if not exists] <name> as <body>`.
var viewDDLPattern = regexp.MustCompile(
	`(?is)^create\s+(?:or\s+(?:alter|replace)\s+)?(materialized\s+)?view\s+(?:if\s+not\s+exists\s+)?(\S+)\s+as\s+(.+)$`)

// viewQuoting strips the quote characters a dialect wraps an identifier in: "x", [x], `x`.
var viewQuoting = strings.NewReplacer(`"`, "", "[", "", "]", "", "`", "")

// NormalizeDDL is canonical, comparable, still-executable DDL: whitespace collapses (layout is not schema),
// and a view create's prefix canonicalizes to lowercase "create [materialized] view <name> as <body>" — `or
// alter`, `or replace`, `if not exists`, and the name's quoting are stripped (ADR-0043), because databases
// rewrite that prefix when storing it and each dialect quotes its own way, while the body is the one SQL the
// entity declares. A view snapshot is therefore dialect-neutral: it renders again from name and body through
// Dialect.CreateViewFromBodySQL. The body is compared as written.
func NormalizeDDL(sql string) string {
	collapsed := strings.Join(strings.Fields(sql), " ")
	match := viewDDLPattern.FindStringSubmatch(collapsed)
	if match == nil {
		return collapsed
	}
	materialized := ""
	if strings.TrimSpace(match[1]) != "" {
		materialized = "materialized "
	}
	return "create " + materialized + "view " + viewQuoting.Replace(match[2]) + " as " + match[3]
}

// ViewParts splits a view create (any dialect's prefix) into its name, body, and whether it is a materialized
// view — the neutral view snapshot's parts, which Dialect.CreateViewFromBodySQL renders again. ok is false for
// text that is no view create.
func ViewParts(ddl string) (name, body string, materialized, ok bool) {
	collapsed := strings.Join(strings.Fields(ddl), " ")
	match := viewDDLPattern.FindStringSubmatch(collapsed)
	if match == nil {
		return "", "", false, false
	}
	return viewQuoting.Replace(match[2]), match[3], strings.TrimSpace(match[1]) != "", true
}

// --- directory-based snapshot lookup (ADR-0017/0018) ------------------------
//
// The diff generator (strictly below the version it regenerates) and the
// shadow replayer's trusted-baseline restore (at or below a --from version)
// both need "the latest snapshot in one object's directory meeting a version
// bound". One scan-and-parse loop serves both (CODING-STANDARD §8).

// snapshotFilesIn lists dir's V*.schema.json files (unsorted; callers pick by version).
func snapshotFilesIn(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "V*.schema.json"))
	return files
}

// latestTableSnapshot scans dir's table snapshots for the one with the
// highest asOfVersion for which accept holds.
func latestTableSnapshot(dir string, accept func(asOfVersion int64) bool) (schema *TableSchema, atVersion int64, ok bool) {
	atVersion = -1
	for _, file := range snapshotFilesIn(dir) {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		candidate, asOfVersion, err := ParseTable(data)
		if err != nil {
			continue
		}
		if accept(asOfVersion) && asOfVersion > atVersion {
			atVersion, schema, ok = asOfVersion, candidate, true
		}
	}
	return schema, atVersion, ok
}

// latestDDLSnapshot scans dir's view/materialized-view DDL snapshots for the
// one with the highest asOfVersion for which accept holds.
func latestDDLSnapshot(dir string, accept func(asOfVersion int64) bool) (snapshot *DDLSnapshot, ok bool) {
	atVersion := int64(-1)
	for _, file := range snapshotFilesIn(dir) {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		candidate, err := ParseDDLSnapshot(data)
		if err != nil {
			continue
		}
		if accept(candidate.AsOfVersion) && candidate.AsOfVersion > atVersion {
			atVersion, snapshot, ok = candidate.AsOfVersion, candidate, true
		}
	}
	return snapshot, ok
}

// LatestTableSnapshotAtOrBefore is dir's latest table snapshot with
// asOfVersion <= version — the shadow replayer's trusted-baseline read: a
// --from version is trusted, never verified below it.
func LatestTableSnapshotAtOrBefore(dir string, version int64) (schema *TableSchema, atVersion int64, ok bool) {
	return latestTableSnapshot(dir, func(v int64) bool { return v <= version })
}

// LatestDDLSnapshotAtOrBefore is dir's latest view/materialized-view DDL
// snapshot with asOfVersion <= version (see LatestTableSnapshotAtOrBefore).
func LatestDDLSnapshotAtOrBefore(dir string, version int64) (snapshot *DDLSnapshot, ok bool) {
	return latestDDLSnapshot(dir, func(v int64) bool { return v <= version })
}
