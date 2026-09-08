package orm

import (
	"io/fs"
	"reflect"
	"strings"
	"sync"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

// SQLSource is where a registry entry's SQL comes from (§7.5): inline text, or
// a file in an embed.FS read once at first use and cached — the Go analog of
// the reference's embedded resource behind a Lazy<string>. A missing file is
// QRY-003 at first use, not at declaration.
type SQLSource struct {
	description string
	load        func() (string, error)
	once        sync.Once
	sql         string
	err         error
}

// SQL is the resolved SQL text.
func (s *SQLSource) SQL() (string, error) {
	s.once.Do(func() { s.sql, s.err = s.load() })
	return s.sql, s.err
}

// Description is the entry's name in error messages: the file path, or a prefix of the inline SQL.
func (s *SQLSource) Description() string { return s.description }

func inlineSource(sql string) *SQLSource {
	head := []rune(sql)
	suffix := ""
	if len(head) > 60 {
		head = head[:60]
		suffix = "…"
	}
	return &SQLSource{
		description: "inline: " + strings.Join(strings.Fields(string(head)), " ") + suffix,
		load:        func() (string, error) { return sql, nil },
	}
}

func embeddedSource(files fs.FS, path string) *SQLSource {
	return &SQLSource{
		description: path,
		load: func() (string, error) {
			data, err := fs.ReadFile(files, path)
			if err != nil {
				return "", core.Errorf("QRY-003", path, "no embedded SQL file at '%s': %s", path, err)
			}
			return string(data), nil
		},
	}
}

// QueryEntry is a registered query: SQL bound once to its args and result types
// (§6). Declare it as a package-level variable next to its args type:
//
//	var UsersByName = orm.Inline[UsersByNameArgs, User]("select … where name like @Pattern")
type QueryEntry[TArgs, TResult any] struct {
	source *SQLSource
}

// Inline declares a query with its SQL at the declaration site (ADR-0009, the primary form).
func Inline[TArgs, TResult any](sql string) QueryEntry[TArgs, TResult] {
	return QueryEntry[TArgs, TResult]{source: inlineSource(sql)}
}

// Embedded declares a query whose SQL is a file in an embed.FS (the optional .sql-file form).
func Embedded[TArgs, TResult any](files fs.FS, path string) QueryEntry[TArgs, TResult] {
	return QueryEntry[TArgs, TResult]{source: embeddedSource(files, path)}
}

// Source is the entry's SQL source.
func (q QueryEntry[TArgs, TResult]) Source() *SQLSource { return q.source }

// ArgsType is the args struct type the placeholders bind from.
func (q QueryEntry[TArgs, TResult]) ArgsType() reflect.Type { return reflect.TypeFor[TArgs]() }

// ResultType is the row type.
func (q QueryEntry[TArgs, TResult]) ResultType() reflect.Type { return reflect.TypeFor[TResult]() }

// CommandEntry is a registered command: SQL bound to its args type; execution returns affected rows.
type CommandEntry[TArgs any] struct {
	source *SQLSource
}

// InlineCommand declares a command with inline SQL.
func InlineCommand[TArgs any](sql string) CommandEntry[TArgs] {
	return CommandEntry[TArgs]{source: inlineSource(sql)}
}

// EmbeddedCommand declares a command whose SQL is a file in an embed.FS.
func EmbeddedCommand[TArgs any](files fs.FS, path string) CommandEntry[TArgs] {
	return CommandEntry[TArgs]{source: embeddedSource(files, path)}
}

// Source is the entry's SQL source.
func (c CommandEntry[TArgs]) Source() *SQLSource { return c.source }

// ArgsType is the args struct type the placeholders bind from.
func (c CommandEntry[TArgs]) ArgsType() reflect.Type { return reflect.TypeFor[TArgs]() }

// ResultType is nil: a command produces no rows.
func (c CommandEntry[TArgs]) ResultType() reflect.Type { return nil }

// Entry is what a Registry lists — a Query or a Command — and what SchemaGuard
// validates (§7.19): the SQL, the args type, and the result type (nil for commands).
type Entry interface {
	Source() *SQLSource
	ArgsType() reflect.Type
	ResultType() reflect.Type
}

// EmptyArgs is the args type for queries and commands that take no parameters.
type EmptyArgs struct{}

// Registry is what an application declares once for SchemaGuard and the CLI —
// explicit registration replaces the reference's assembly scanning
// (CODING-STANDARD §10): the mapped entity types, the registered queries and
// commands, the migration versions, and the embedded schema snapshots.
//
//	var App = orm.Registry{
//	    Entities:   []reflect.Type{orm.TypeOf[User](), orm.TypeOf[Role]()},
//	    Entries:    []orm.Entry{UsersByName, MarkShipped},
//	    Migrations: migrationSet,          // orm.NewMigrationSet(V0001{}, V0002{}, …)
//	    Snapshots:  migrations.Snapshots,  // //go:embed Table/*/*.schema.json View/*/*.schema.json
//	}
type Registry struct {
	// Entities are the mapped types: every entity SchemaGuard checks against the database.
	Entities []reflect.Type
	// Entries are the registered queries and commands.
	Entries []Entry
	// Migrations is the application's migration versions; nil means none (MIG-030 never fires).
	Migrations *migrations.Set
	// Snapshots holds the V000N.schema.json files rollbacks derive from (ADR-0018) — the
	// application's embed.FS; nil means none embedded (the CLI's --snapshots reads files instead).
	Snapshots fs.FS
}
