package sqlite

// The SQLite shadow host (ADR-0043, spec/dialects.md "Migration tooling"): the throwaway database is a temp
// file, dropped (with its -wal/-shm/-journal siblings) afterwards. The replay itself lives in the migrations
// package and runs on every dialect; [Shadow] is the SQLite spelling of it, kept for callers that have only a
// migration set.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

// ShadowHost provisions a temp-file SQLite database for the shadow replay; it also reads a trigger back from
// sqlite_master (SQLite stores the text as submitted, so the read-back is lossless).
type ShadowHost struct{}

var (
	_ migrations.ShadowHost      = ShadowHost{}
	_ migrations.TriggerReadBack = ShadowHost{}
)

// Provision creates the temp file's connection string; the admin connection is ignored.
func (ShadowHost) Provision(_ context.Context, _ string) (string, func(context.Context) error, error) {
	file := filepath.Join(os.TempDir(), "simpleorm-shadow-"+randomHex(16)+".db")
	drop := func(context.Context) error {
		// Best effort: Windows can briefly hold a delete lock after the driver closes its handle.
		_ = os.Remove(file)
		_ = os.Remove(file + "-wal")
		_ = os.Remove(file + "-shm")
		_ = os.Remove(file + "-journal")
		return nil
	}
	return "Data Source=" + file, drop, nil
}

// ReadBackTrigger is ParseTriggerRows: the inverse of CreateTriggerSQL over the stored text.
func (ShadowHost) ReadBackTrigger(name string, rows []string) (*migrations.TriggerDeclaration, string) {
	return ParseTriggerRows(name, rows)
}

// ShadowOptions configures [Shadow]: the migration set to replay, every entity Shadow may need to resolve a
// touched relation to its mapped type, the migrations tree's directory, and the version range (To == 0 means
// unbounded).
type ShadowOptions struct {
	Set      *migrations.Set
	Entities []reflect.Type
	// Triggers are the registry's trigger classes (ADR-0042).
	Triggers []reflect.Type
	OutDir   string
	From     int64
	To       int64
}

// ShadowResult is [Shadow]'s outcome: files written and progress notes, in the order the replay produced them.
type ShadowResult = migrations.ShadowResult

// Shadow replays Set into a throwaway SQLite database and rewrites the snapshots (migrations.Shadow with the
// SQLite dialect and host).
func Shadow(ctx context.Context, options ShadowOptions) (*ShadowResult, error) {
	return migrations.Shadow(ctx, migrations.ShadowOptions{
		Dialect: New(), Host: ShadowHost{}, Set: options.Set, Entities: options.Entities, Triggers: options.Triggers,
		OutDir: options.OutDir, From: options.From, To: options.To,
	})
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf) // crypto/rand.Read only fails when the OS entropy source is broken; a fallback name would collide just as rarely.
	return hex.EncodeToString(buf)
}
