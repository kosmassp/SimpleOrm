package migrations

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// SnapshotEntry is one object's snapshot at one version: exactly one of Table
// (a table snapshot) or DDL (a view/materialized-view/procedure snapshot) is set.
type SnapshotEntry struct {
	Table *TableSchema
	DDL   string
}

// SnapshotSet is every committed schema snapshot of a migrations tree,
// indexed by (object, version) — the history a (future) runner derives
// rollbacks from (ADR-0018). Object names compare case-insensitively, as in
// the C# reference.
type SnapshotSet struct {
	byObject map[string]map[int64]SnapshotEntry
}

func newSnapshotSet() *SnapshotSet {
	return &SnapshotSet{byObject: map[string]map[int64]SnapshotEntry{}}
}

// SnapshotsFromFS reads every *.schema.json under files — the Go analog of
// the reference's embedded resources: an application passes its own
// `//go:embed Table/*/*.schema.json View/*/*.schema.json` FS.
func SnapshotsFromFS(files fs.FS) (*SnapshotSet, error) {
	set := newSnapshotSet()
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !isSchemaJSON(entry.Name()) {
			return nil
		}
		data, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		return set.add(data)
	})
	if err != nil {
		return nil, err
	}
	return set, nil
}

// SnapshotsFromDirectory reads every *.schema.json recursively under dir; a
// missing directory is an empty set (the dev-workflow / CLI `--snapshots` path).
func SnapshotsFromDirectory(dir string) (*SnapshotSet, error) {
	set := newSnapshotSet()
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return set, nil
	}
	dirFS := os.DirFS(dir)
	err = fs.WalkDir(dirFS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !isSchemaJSON(entry.Name()) {
			return nil
		}
		data, err := fs.ReadFile(dirFS, path)
		if err != nil {
			return err
		}
		return set.add(data)
	})
	if err != nil {
		return nil, err
	}
	return set, nil
}

func isSchemaJSON(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".schema.json")
}

func (s *SnapshotSet) add(data []byte) error {
	var probe struct {
		Object      string  `json:"object"`
		AsOfVersion int64   `json:"asOfVersion"`
		DDL         *string `json:"ddl"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("parse schema snapshot: %w", err)
	}
	key := strings.ToLower(probe.Object)
	if s.byObject[key] == nil {
		s.byObject[key] = map[int64]SnapshotEntry{}
	}
	if probe.DDL != nil {
		_, _, ddl, asOfVersion, err := ParseDDL(data)
		if err != nil {
			return err
		}
		s.byObject[key][asOfVersion] = SnapshotEntry{DDL: ddl}
		return nil
	}
	schema, asOfVersion, err := ParseTable(data)
	if err != nil {
		return err
	}
	s.byObject[key][asOfVersion] = SnapshotEntry{Table: schema}
	return nil
}

// Count is the total number of snapshot entries across every object.
func (s *SnapshotSet) Count() int {
	total := 0
	for _, versions := range s.byObject {
		total += len(versions)
	}
	return total
}

// At returns the object's snapshot at exactly this version, or nil when the version didn't touch it.
func (s *SnapshotSet) At(object string, version int64) *SnapshotEntry {
	versions, ok := s.byObject[strings.ToLower(object)]
	if !ok {
		return nil
	}
	entry, ok := versions[version]
	if !ok {
		return nil
	}
	return &entry
}

// LatestBefore returns the object's latest snapshot strictly before version —
// nil means the version created the object.
func (s *SnapshotSet) LatestBefore(object string, version int64) *SnapshotEntry {
	versions, ok := s.byObject[strings.ToLower(object)]
	if !ok {
		return nil
	}
	var latest *SnapshotEntry
	var latestVersion int64
	for v, entry := range versions {
		if v >= version {
			continue
		}
		if latest == nil || v > latestVersion {
			entryCopy := entry
			latest = &entryCopy
			latestVersion = v
		}
	}
	return latest
}
