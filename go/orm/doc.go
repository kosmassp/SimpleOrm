// Package orm is the Go port of SimpleOrm, Levels 0–1 (ADR-0027): a SQL-first
// micro-ORM on SQLite — metadata from struct tags and a small descriptor,
// strict row mapping, @name parameters, a session with generated CRUD and
// explicit transactions, the criteria core rendered by the dialect,
// SchemaGuard, and code migrations. It shares no code with the C# reference
// under dotnet/; it shares ../spec and ../conformance and is correct when it
// passes the same conformance files unchanged (CLAUDE.md §12).
//
// This package is the whole public surface for application code — the C#
// SimpleOrm namespace: the session (Open, Query, Insert, Get, From …), the
// registry (Inline, Embedded — the entry types are QueryEntry and
// CommandEntry, since Go cannot give a type and a function the one name
// Query), and aliases of the spec-level contracts that live in
// internal/core (Error, EntityMap, Decimal, Criteria factories, the Dialect
// seam). orm/sqlite is the SQLite dialect and orm/cli the command.
// How Go differs from the reference — and only there — is the §10 table in
// CODING-STANDARD.md.
package orm
