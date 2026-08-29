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
| `migrations-cases/` | runner scenarios: a migration set as data plus `{ "command", "expect": { "applied" } \| { "error" } }` steps (format in spec/migrations.md) | 5 (live) |
| `ast/` | query AST with expected SQL per dialect | Level 2 |

Rules:

- Expected values use a small documented JSON encoding for dates, decimals, GUIDs, and
  byte arrays (documented here when `cases/` lands in milestone 4).
- Error codes are the cross-language contract for failures.
- A feature without a conformance case is not done.
