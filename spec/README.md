# SimpleOrm spec

Language-neutral specification of SimpleOrm, written as each level stabilizes. Ports
(Go, Java, PHP) implement this spec and prove themselves against `../conformance/`;
they share no code with the C# reference implementation.

Planned documents (each lands with the milestone that stabilizes it):

| File | Content | Milestone |
|---|---|---|
| `metadata-model.md` | `EntityMap`: what it contains, JSON export format | 2 (done) |
| `mapping-rules.md` | naming conventions, construction, conversions, strictness | 4 (done) |
| `errors.md` | error code registry (`MAP-`, `PRM-`, `MIG-`, `VAL-`, `CRUD-`, `TX-`) | 2+ (live; codes registered before rules are implemented) |
| `migrations.md` | file format, version table, checksums, locking, up/down semantics | 5 |
| `validation-rules.md` | every SchemaGuard rule with its error code | 6 |
| `query-ast.md` | query AST | Level 2 |

Rules:

- Every new error code is registered in `errors.md` before it gets a message.
- Error codes are the cross-language contract: messages may differ per language, codes may not.
- Spec documents grow with every milestone, not at the end.
