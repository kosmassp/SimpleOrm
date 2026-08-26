# SimpleOrm conformance suite

The executable definition of the library. Every implementation (C# reference, future
Go/Java/PHP ports) runs the same files; a port is correct when it passes them
unchanged. All formats are JSON so any language can load them.

Layout (populated milestone by milestone):

| Directory | Content | Milestone |
|---|---|---|
| `schema/postgres/` | migrations for the fixture database | 3+ |
| `fixtures/` | seed data | 3+ |
| `entities/` | expected `EntityMap` JSON for the fixture entities | 2 |
| `cases/` | query/command/error cases: `{ "name", "query", "params", "expect": { "rows" } \| { "error": "MAP-001" } }` | 4 |
| `migrations-cases/` | runner scenarios: migration files plus `{ "preState", "command", "expectStatus" \| "error" }` | 5 |
| `ast/` | query AST with expected SQL per dialect | Level 2 |

Rules:

- Expected values use a small documented JSON encoding for dates, decimals, GUIDs, and
  byte arrays (documented here when `cases/` lands in milestone 4).
- Error codes are the cross-language contract for failures.
- A feature without a conformance case is not done.
