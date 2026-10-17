# SimpleOrm spec

Language-neutral specification of SimpleOrm, written as each level stabilizes. Ports
(PHP and Go today; Rust, Python, and TypeScript next) implement this spec and prove
themselves against `../conformance/`; they share no code with the C# reference
implementation. A level is closed when a port built from these documents and the
conformance files alone — its agents forbidden to read any other implementation —
passes the same files (ADR-0031, ADR-0034).

| File | Content | Level |
|---|---|---|
| `metadata-model.md` | `EntityMap`: relation sources, keys, columns, relationships, owned types, cascades, hooks, declared renames; the JSON export format | 1–3 |
| `mapping-rules.md` | naming conventions, construction, conversions, strictness, the conformance value encoding | 1 |
| `errors.md` | error code registry (`MAP-`, `PRM-`, `MIG-`, `VAL-`, `CRUD-`, `QRY-`, `TX-`, `REL-`, `DDL-`, `UOW-`) | all (live; codes registered before rules are implemented) |
| `session.md` | lifetime, registry surface, parameters, transactions, async; **Tracking** — the identity map and change snapshots | 1, 3 |
| `crud.md` | generated CRUD by key, key strategies, column-list update, optimistic concurrency | 1 |
| `migrations.md` | versioned code model, version table, checksums, run lock, derived downs, snapshots, the diff generator; **Draft migrations** — declared renames and the draft marker | 1, 3 |
| `validation-rules.md` | every SchemaGuard rule with its error code | 1 |
| `query-ast.md` | the criteria AST, null semantics, dialect rendering, joins and projection | 1–2 |
| `loading.md` | relationship loading: explicit, batch, and the three eager fetch modes | 2 |
| `unit-of-work.md` | `Persist`/`Remove`/`Flush`, order of writes, declared cascades; detach and merge | 3 |
| `hooks.md` | declared hooks: created-at, updated-at, soft delete; the session clock | 3 |
| `dialects.md` | the dialect seam member by member; the SQLite, SQL Server, and PostgreSQL realizations: capability flags, paging, generated keys, DDL, migration actions, the run lock, storage types, declared-type compatibility, temporals, array parameters, introspection shapes, the live-testing contract | 1, 4 (dialects pulled forward by ADR-0024/0025) |

Rules:

- Every new error code is registered in `errors.md` before it gets a message.
- Error codes are the cross-language contract: messages may differ per language, codes may not.
- Spec documents grow with every milestone, not at the end.
- A port's unanswered questions are recorded, never resolved by reading another
  implementation; each answer lands under the document's "Clarifications" and no
  rule changes without an ADR in `../docs/decisions.md`.
