# Validation rules — SchemaGuard

`SchemaGuard.ValidateAsync(db, assembly, ct)` validates everything the assembly
declares against the real database the session is connected to. It runs at startup
and in a test calling the same code (§7.18); it fails fast — no warn-only mode
(§7.21); and it always produces the **complete report**: every violation across
every source, one `SchemaValidationException`, never first-error-only (§7.20).

Nothing validated is executed with effect: statements are prepared/described inside
a transaction that is always rolled back.

## What is validated

1. **Registry entries** — every `public static` `Query<TArgs,TResult>` /
   `Command<TArgs>` field in the assembly's exported types.
2. **Entities** — every exported type carrying mapping metadata. Tables check
   existence, columns, declared types, and nullability; views and materialized
   views check existence and columns only (they report neither declared types nor
   nullability reliably); statement entities validate their defining SQL like a
   query; sources the dialect cannot host (materialized views, procedures on
   SQLite) are skipped — dormant, per their capability flags.
3. **Migrations** — the assembly's versions vs the database's recorded history:
   pending versions are `MIG-030`; drifted checksums `MIG-010`; history unknown to
   the code `MIG-011`.

## Rules per statement

| Code | Rule |
|---|---|
| `VAL-001` | the SQL fails to prepare |
| `PRM-001` / `PRM-002` | placeholders ↔ args-type properties, both directions, statically |
| `MAP-001` / `MAP-002` / `MAP-003` | result columns ↔ result type exactly, via the one mapping pipeline |
| `VAL-010` | a nullable column mapped to a non-nullable member |
| `VAL-011` | a column's declared type incompatible with the member type and no handler registered |
| `VAL-020` | lint: `current_timestamp` / `datetime('now')` — they store datetimes without a UTC marker |
| `VAL-021` | lint: `SELECT *` (aggregate `count(*)` is not a star selection) |

**Origin rules.** A result column traced to a base table column (driver origin
metadata) is checked against that column's declared type and nullability; a
primary-key column counts as NOT NULL even where the engine reports otherwise.
An **expression column** has unknowable nullability: the member must be nullable
unless the SQL carries the override comment

```sql
select count(*) as n from users -- notnull: n, other_col
```

(case-insensitive, comma-separated, repeatable). Note the deliberate imprecision
inherited from engines: a NOT NULL base column can still yield NULL through an
outer join; the origin-based check trusts the base constraint (§7.19).

## Rules per entity (tables)

| Code | Rule |
|---|---|
| `VAL-012` | the mapped relation does not exist |
| `VAL-013` | a mapped column does not exist in the relation |
| `VAL-011` / `VAL-010` | declared-type compatibility and nullability per column, honoring `[EnumAsInt]` and registered handlers |

The declared-type table is the dialect's (§7.25); on SQLite it is meaningful
because all fixture and migration tables are STRICT. Loader failures
(`MAP-010`…) surface in the same report.

## Introspection

The dialect supplies `ColumnsInfoSql` (`@relation` → `name, type, notnull, pk`;
empty = missing) — the same introspection surface the migration diff generator
builds on.
