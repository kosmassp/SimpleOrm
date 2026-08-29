# SimpleOrm conformance suite

The executable definition of the library. Every implementation (C# reference, future
Go/Java/PHP ports) runs the same files; a port is correct when it passes them
unchanged. All formats are JSON so any language can load them.

Layout (populated milestone by milestone):

| Directory | Content | Milestone |
|---|---|---|
| `schema/sqlite/` | migrations for the fixture database | 3+ |
| `entities/` | expected `EntityMap` JSON for the fixture entities (regenerate: run tests with `SIMPLEORM_CONFORMANCE_WRITE=1`) | 2 (done) |
| `cases/` | query/error cases: `{ "name", "result", "query", "expect": { "rows" } \| { "error": "MAP-001" } }` — value encoding defined in spec/mapping-rules.md | 4 (live) |
| `fixtures/` | seed data (`seed.json`) applied before each case | 4 (live) |
| `migrations-cases/` | runner scenarios: a migration set as data plus `{ "command", "expect": { "applied" } \| { "error" } }` steps (format in spec/migrations.md). ADR-0017/18 additions: top-level `snapshots` (the derived-rollback history), step `renames` (typed pairs the derived rollback inverts) and `expectDefinition` (the MIG-012 view guard), a raw `sql` command (the outside hotfix), `force` on migrate/down, and deep expects `columns` (name-sorted per table) and `ddl` (normalized per view) | 5 (live) |
| `crud-cases/` | generated CRUD + concurrency scenarios: insert/get/update/delete steps with snapshots (`as`/`from`), `$last` keys, and expected values or error codes | 7 (live) |
| `diff-cases/` | generator diff scenarios (ADR-0017): `{ "current", "snapshot" \| null, "renames", "expect": { "isNew", "added", "removed", "renamed", "addedIndexes", "removedIndexes", "unsupported" } }` — both shapes in snapshot form, pure data, no database. `addedIndexes` entries are index names that must appear in the emitted create SQL; `unsupported` entries are substrings (usually the column name): messages differ per language, what they name may not | ADR-0017 (live) |
| `snapshot-cases/` | exact snapshot exports (ADR-0017): `{ "entity", "kind"?, "asOfVersion", "generatedAt", "expect": <the full document> }` — each implementation exports its native fixture entity at the pinned time and must produce the expected document exactly (tables by columns, views by normalized DDL) | ADR-0017 (live) |
| `ast/` | criteria query AST as JSON with the exact expected SQL and ordered parameter values per dialect, or an error code (`QRY-006`/`QRY-007`) — rendering is pure, no database. Ops: `eq ne gt ge lt le like in is_null is_not_null and or not`; `select` carries `where` (implicitly ANDed), `orderBy` (`property` + optional `order: "desc"`), `limit`, `offset`; parameters are `@c0…` in render order (WHERE, then limit, then offset) | L2 M2 (live) |

Rules:

- Expected values use a small documented JSON encoding for dates, decimals, GUIDs, and
  byte arrays (documented here when `cases/` lands in milestone 4).
- Error codes are the cross-language contract for failures.
- A feature without a conformance case is not done.
