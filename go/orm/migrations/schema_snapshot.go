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

// TableColumn is one column of a TableSchema, in storage terms.
type TableColumn struct {
	Name        string
	StorageType string
	Nullable    bool
	Key         bool
	Generated   bool
}

// IndexPart is one column of a TableIndex, in index order.
type IndexPart struct {
	ColumnName string
	Descending bool
}

// TableIndex is one index of a TableSchema.
type TableIndex struct {
	Name    string
	Columns []IndexPart
	Unique  bool
}

// TableSchema is a table's shape, provider-agnostic but in storage types
// (what CREATE TABLE emits) — the per-table schema snapshot, format v2
// (ADR-0013 add.3, ADR-0017). Both snapshot producers build this: the
// metadata exporter (a later `snapshot` command) and a (later) shadow
// replayer's introspection — which is what makes the two comparable.
type TableSchema struct {
	Name    string
	Columns []TableColumn
	Indexes []TableIndex
}

// FromMap is the current model rendered as a schema (the metadata-side producer).
func FromMap(m *core.EntityMap, dialect core.Dialect) *TableSchema {
	columns := make([]TableColumn, len(m.Properties))
	for i, p := range m.Properties {
		columns[i] = TableColumn{
			Name: p.ColumnName, StorageType: dialect.StorageType(p), Nullable: p.IsNullable,
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
	return &TableSchema{Name: m.RelationName, Columns: columns, Indexes: indexes}
}

// Export renders map's current shape as a format-v2 table snapshot document (see ExportSchema).
func Export(m *core.EntityMap, dialect core.Dialect, asOfVersion int64, generatedAt time.Time) string {
	return ExportSchema(FromMap(m, dialect), asOfVersion, generatedAt)
}

// ExportSchema is the format-v2 document for schema (ADR-0017): object,
// asOfVersion, generatedAt, then columns and indexes name-sorted (ordinal byte
// order) for determinism — byte-identical to the C# reference's export.
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
	doc = doc.Set("asOfVersion", asOfVersion)
	doc = doc.Set("generatedAt", core.FormatUTC(generatedAt))
	doc = doc.Set("columns", columnsJSON)
	doc = doc.Set("indexes", indexesJSON)
	return core.CanonicalJSON(doc)
}

// ExportDDL is the DDL-shaped snapshot for view-, materialized-view-, and
// procedure-backed objects (ADR-0017 add.1): those self-reflect from their
// defining SQL, so their history is compared by (normalized) DDL, not columns.
func ExportDDL(objectName, kind, ddl string, asOfVersion int64, generatedAt time.Time) string {
	doc := core.JSONObject{}
	doc = doc.Set("object", objectName)
	doc = doc.Set("kind", kind)
	doc = doc.Set("asOfVersion", asOfVersion)
	doc = doc.Set("generatedAt", core.FormatUTC(generatedAt))
	doc = doc.Set("ddl", NormalizeDDL(ddl))
	return core.CanonicalJSON(doc)
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
	AsOfVersion int64                     `json:"asOfVersion"`
	Columns     []tableSnapshotColumnJSON `json:"columns"`
	Indexes     []tableSnapshotIndexJSON  `json:"indexes"`
}

// ParseTable reads a format-v2 table snapshot document.
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
	return &TableSchema{Name: doc.Object, Columns: columns, Indexes: indexes}, doc.AsOfVersion, nil
}

// ParseDDL reads a DDL-shaped snapshot document.
func ParseDDL(data []byte) (object, kind, ddl string, asOfVersion int64, err error) {
	var doc struct {
		Object      string `json:"object"`
		Kind        string `json:"kind"`
		AsOfVersion int64  `json:"asOfVersion"`
		DDL         string `json:"ddl"`
	}
	if err = json.Unmarshal(data, &doc); err != nil {
		return "", "", "", 0, fmt.Errorf("parse ddl snapshot: %w", err)
	}
	return doc.Object, doc.Kind, doc.DDL, doc.AsOfVersion, nil
}

var viewDDLPattern = regexp.MustCompile(
	`(?i)^create\s+(materialized\s+)?view\s+(if\s+not\s+exists\s+)?(\S+)\s+as\s+(.+)$`)

// NormalizeDDL is canonical, comparable, still-executable DDL: whitespace
// collapses (layout is not schema — mirrors the reference's Split(null,
// RemoveEmptyEntries) + Join(" ")), and a view create's prefix canonicalizes
// to lowercase "create [materialized] view <name> as" — databases rewrite
// that prefix when storing it (SQLite drops IF NOT EXISTS and recases CREATE
// VIEW), so the rendered and the introspected form must meet in the middle.
// The name and body are compared as written.
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
	return "create " + materialized + "view " + match[3] + " as " + match[4]
}

// --- directory-based snapshot lookup (ADR-0017/0018) ------------------------
//
// The diff generator (strictly below the version it regenerates) and the
// shadow replayer's trusted-baseline restore (at or below a --from version,
// orm/sqlite/shadow.go) both need "the latest snapshot in one object's
// directory meeting a version bound". One scan-and-parse loop serves both
// (CODING-STANDARD §8).

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
func latestDDLSnapshot(dir string, accept func(asOfVersion int64) bool) (object, ddl string, atVersion int64, ok bool) {
	atVersion = -1
	for _, file := range snapshotFilesIn(dir) {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		parsedObject, _, parsedDDL, asOfVersion, err := ParseDDL(data)
		if err != nil {
			continue
		}
		if accept(asOfVersion) && asOfVersion > atVersion {
			object, ddl, atVersion, ok = parsedObject, parsedDDL, asOfVersion, true
		}
	}
	return object, ddl, atVersion, ok
}

// LatestTableSnapshotAtOrBefore is dir's latest table snapshot with
// asOfVersion <= version — the shadow replayer's trusted-baseline read: a
// --from version is trusted, never verified below it.
func LatestTableSnapshotAtOrBefore(dir string, version int64) (schema *TableSchema, atVersion int64, ok bool) {
	return latestTableSnapshot(dir, func(v int64) bool { return v <= version })
}

// LatestDDLSnapshotAtOrBefore is dir's latest view/materialized-view DDL
// snapshot with asOfVersion <= version (see LatestTableSnapshotAtOrBefore).
func LatestDDLSnapshotAtOrBefore(dir string, version int64) (object, ddl string, atVersion int64, ok bool) {
	return latestDDLSnapshot(dir, func(v int64) bool { return v <= version })
}
