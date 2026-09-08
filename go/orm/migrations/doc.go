// Package migrations is versioned code migrations (CLAUDE.md §7.22-§7.24;
// ADR-0013, ADR-0016, ADR-0017 + addenda, ADR-0018): root versions composing
// per-object steps, table/view actions with their fixed execution order and
// per-action data hooks, schema snapshots (format v2) and the set that indexes
// them, the DDL a snapshot renders straight to, and the diff generator core
// with its Go-source emitters. Migrations are code, never external .sql files;
// applying them (the runner), deriving rollbacks from snapshots, force sync,
// and the `diff --amend` file surgery are a later phase built on these types.
//
// Application code reaches this package through the orm package's aliases
// (CODING-STANDARD §1, §10) — a migrations tree such as orm/sample/migrations
// imports only orm, never this package directly.
package migrations
