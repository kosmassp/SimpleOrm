# Error code registry

Error codes are the cross-language contract for failures: messages may differ per
implementation, codes may not. Every rule gets its code registered here **before**
any implementation gives it a message (CLAUDE.md §13). Conformance cases reference
these codes.

## Prefixes

| Prefix | Domain |
|---|---|
| `MAP-` | metadata loading and row↔object mapping |
| `PRM-` | parameter binding and declaration |
| `VAL-` | SchemaGuard validation and lint |
| `MIG-` | migrations |
| `CRUD-` | generated CRUD |
| `QRY-` | query execution |
| `DDL-` | schema generation from metadata |
| `TX-` | transactions and session |

## MAP — metadata loading and mapping

| Code | Rule | Origin | Enforced |
|---|---|---|---|
| `MAP-001` | result column has no matching mapped property | §7.7 | milestone 4/6 |
| `MAP-002` | required property has no matching result column | §7.7 | milestone 4/6 |
| `MAP-003` | ambiguous construction (competing constructors) | §7.8 | milestone 4 |
| `MAP-010` | public settable property carries none of `[Column]`, `[Ignore]`, or a relationship attribute | ADR-0004/0005 | milestone 2 |
| `MAP-011` | `[ManyToOne]` navigation property exposes a public setter | ADR-0005 add.2 | milestone 2 |
| `MAP-012` | class carries more than one relation source (`[Table]`/`[View]`/`[MaterializedView]`/`[Statement]`/`[Procedure]`) | ADR-0008 | milestone 2 |
| `MAP-013` | attribute illegal for the relation source (`[Generated]`/`[Version]` on any non-table; `[Key]` on statement/procedure) | ADR-0008 | milestone 2 |
| `MAP-014` | `[Index]` on a relation source that cannot carry one (view, statement, procedure) | ADR-0007/0008 | milestone 2 |
| `MAP-015` | invalid `[Index]` column stream (empty; leading/doubled `SortOrder`; token neither string nor `SortOrder`; unknown or unmapped property) | ADR-0007 add.3 | milestone 2 |
| `MAP-016` | `[ManyToOne]` foreign-key property name unknown or unmapped | ADR-0005 | milestone 2 |
| `MAP-017` | invalid `[Statement]` parameter declaration (odd token count; token neither name string nor `Type`; duplicate name) | ADR-0008 add.2 | milestone 2 |
| `MAP-018` | two properties map to the same column name | ADR-0004 | milestone 2 |
| `MAP-019` | no key defined where one is required, or `[Generated]`/`[Version]` on a property without `[Column]` | §7.1 | milestone 2 |

## PRM — parameters

| Code | Rule | Origin | Enforced |
|---|---|---|---|
| `PRM-001` | SQL parameter has no matching args property | §7.13 | milestone 3/6 |
| `PRM-002` | args property never used by the SQL | §7.13 | milestone 3/6 |
| `PRM-010` | `@placeholder` in `[Statement]` SQL not declared in the attribute | ADR-0008 add.2 | milestone 2 |
| `PRM-011` | `[Statement]` declared parameter not used by its SQL | ADR-0008 add.2 | milestone 2 |
| `PRM-012` | args property type differs from the statement's declared parameter type | ADR-0010 | milestone 3 |

## QRY — query execution

| Code | Rule | Origin | Enforced |
|---|---|---|---|
| `QRY-001` | `QuerySingleAsync` found no rows | §6 | milestone 3 |
| `QRY-002` | `QuerySingleAsync` found more than one row | §6 | milestone 3 |
| `QRY-003` | embedded SQL resource not found for a registered query | §7.5 | milestone 3 |
| `QRY-004` | statement execution requested for a type that is not statement-backed | ADR-0010 | milestone 3 |

## DDL — schema generation from metadata

| Code | Rule | Origin | Enforced |
|---|---|---|---|
| `DDL-001` | schema creation requested for a relation source it does not apply to | ADR-0011 | milestone 3 |
| `DDL-002` | materialized-view creation on a dialect without materialized views | ADR-0008 add.3 | milestone 3 |

## TX — transactions and session

| Code | Rule | Origin | Enforced |
|---|---|---|---|
| `TX-001` | `BeginAsync` while a transaction is already active on the session | §7.17 | milestone 3 |

## VAL — SchemaGuard (registered now, enforced milestone 6)

| Code | Rule | Origin |
|---|---|---|
| `VAL-001` | SQL fails to prepare | §7.19 |
| `VAL-010` | nullable column mapped to non-nullable property | §7.19 |
| `VAL-011` | column declared type incompatible with property type / no handler | §7.19 |
| `VAL-020` | non-UTC / non-ISO date storage | §7.19, ADR-0003 |
| `VAL-021` | `SELECT *` in registered SQL | §7.19 |

## MIG — migrations (registered now, enforced milestone 5)

| Code | Rule | Origin |
|---|---|---|
| `MIG-010` | applied migration checksum changed | §7.23 |
| `MIG-020` | `migrate down` past a version with no down file | §7.22 |
| `MIG-030` | pending migrations at validation time | §7.24 |

## CRUD — generated CRUD (registered now, enforced milestone 7)

| Code | Rule | Origin |
|---|---|---|
| `CRUD-001` | `GetAsync` found no row for the key | ADR-0006 |
| `CRUD-002` | key shape mismatch (arity, order, or types vs. `EntityMap` key) | ADR-0006 |
| `CRUD-003` | write attempted on a read-only relation source | ADR-0008 |
| `CRUD-004` | `[ManyToOne]` navigation key disagrees with FK property on write | ADR-0005 add.1 |
| `CRUD-010` | optimistic concurrency conflict (zero rows affected with version column) | §7.16 |

Codes are append-only: a retired rule keeps its code (marked retired), never reuses it.
