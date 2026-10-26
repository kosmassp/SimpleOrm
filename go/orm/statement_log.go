package orm

import (
	"fmt"
	"os"
	"reflect"
)

// The statement log (Level 4 item 4, ADR-0045, spec/session.md "Statement log"): Options.Log receives one line per
// statement the session sends — after it is bound, before it executes — and nothing is formatted while it is nil.
// ConsoleLog is the one built-in sink; forwarding to the application's own logger is a one-line adapter:
//
//	orm.Options{Dialect: d, Log: func(line string) { logger.Debug(line) }}

// ConsoleLog writes each statement line to standard output (the Go spelling of C# DbLog.Console): assign it to
// Options.Log to switch the log on with one line.
func ConsoleLog(line string) { fmt.Fprintln(os.Stdout, line) }

// logStatement writes the line of one statement the session sends; with no Options.Log it does nothing and formats nothing.
func (db *Db) logStatement(name, sqlText string, args []any) {
	db.log.Statement(name, sqlText, args)
}

// logName is the statement-log name of a registry entry: the name the entry declares (Named), else the resource path
// of an embedded entry, else the kind of entry — `query` or `command`; an inline entry's description would only repeat
// the SQL (spec/session.md "Statement log").
func (s *SQLSource) logName(kind string) string {
	switch {
	case s.name != "":
		return s.name
	case s.embedded:
		return s.description
	}
	return kind
}

// logName is the statement-log name `<Entity>.<operation>`; empty while no log is set, so the hot paths build nothing.
func (db *Db) logName(entityType reflect.Type, operation string) string {
	if db.log == nil {
		return ""
	}
	return entityType.Name() + "." + operation
}
