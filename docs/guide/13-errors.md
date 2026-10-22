# Errors

Every failure the library raises carries a stable code, the thing it names
(a query, a property, a parameter, a session), and a message saying what was
expected. Codes are the cross-language contract: messages may differ per
implementation, codes may not. The full registry, append-only, is
[`spec/errors.md`](../../spec/errors.md).

## How errors are shaped

In C#, `SimpleOrmException` carries `Code`, `Target`, and `Message`. Three
sibling types exist for cases you catch differently: `ConcurrencyException`
(`Code` is always `CRUD-010`), `MappingException` (a loader failure, with
`EntityType` and the complete list of `MappingError`s for the type), and
`SchemaValidationException` (the complete SchemaGuard report as `Errors`,
each with `Code`, `Source`, `Message`).

```csharp
var exception = await Assert.ThrowsAsync<SimpleOrmException>(() => db.BeginAsync(CancellationToken.None));
Assert.Equal("TX-001", exception.Code);
```

In PHP, `SimpleOrmException` exposes `->errorCode`; `ConcurrencyException`,
`MappingException`, and `SchemaValidationException` mirror the C# types.

```php
try {
    $transactions->update($stale);
} catch (ConcurrencyException $conflict) {
    expect($conflict->errorCode === 'CRUD-010', 'CRUD-010 on a stale version');
}
```

In Go, errors are `*orm.Error` values with `Code`, `Target`, `Message`, and a
`Cause` (the wrapped provider error, when there is one); match with
`errors.As` or read the code with `orm.CodeOf(err)`:

```go
var ormErr *orm.Error
if errors.As(err, &ormErr) && ormErr.Code == "CRUD-010" { ... }
```

Provider errors (a constraint violation, a lost connection) are never wrapped
in a SimpleOrm code; cancellation surfaces as the platform's signal.

## The prefixes

| Prefix | Domain |
|---|---|
| `MAP-` | metadata loading and row-to-object mapping |
| `PRM-` | parameter binding and declaration |
| `QRY-` | query execution and criteria |
| `REL-` | relationship loading |
| `CRUD-` | generated writes and key reads |
| `TX-` | transactions and session |
| `VAL-` | SchemaGuard validation and lint |
| `MIG-` | migrations |
| `DDL-` | schema generation from metadata |
| `UOW-` | tracking and the unit of work |

## The common codes and their fixes

| Code | You hit it when | Fix |
|---|---|---|
| `MAP-001` | a result column has no mapped property | list exactly the mapped columns, or map to a DTO |
| `MAP-002` | a mapped or required property has no result column | add the column to the SQL, or make the DTO member optional |
| `MAP-003` | two DTO constructors match the same column count | keep one public constructor |
| `MAP-010` | a public settable property has no `[Column]`, `[Ignore]`, or relationship attribute | mark it; mapping is opt-in |
| `MAP-011` | a navigation has a public setter | make it `private set` (PHP `private(set)`) |
| `MAP-012` | a class carries two relation sources | keep one of `[Table]`/`[View]`/`[Statement]`/... |
| `MAP-013` / `MAP-014` | `[Generated]`, `[Version]`, `[Key]`, or `[Index]` on a source that cannot carry it | remove it from the view or statement |
| `MAP-016` / `MAP-021` / `MAP-022` | an FK list does not match a key's arity or names an unknown property | one entry per key part, in key order |
| `MAP-018` | two properties map to one column | rename one, or change an owned prefix |
| `MAP-019` | no key, a generated composite or non-integer key, or `[Generated]`/`[Version]` without `[Column]` | fix the key declaration |
| `MAP-020` | a collection navigation that is not `IEnumerable<T>` of an entity, or a one-to-one that is a collection | fix the property type |
| `MAP-024` | an invalid owned-type declaration | only `[Column]` members, no key or relationship, a parameterless constructor |
| `MAP-025` | `[Cascade]` anywhere but a many-to-one, one-to-one, or one-to-many navigation | move or remove it |
| `MAP-026` | a misplaced hook | a temporal property with `[Column]` on a table; `bool` for `[SoftDelete]` |
| `MAP-027` | a misplaced `[RenamedFrom]` | only on a `[Column]` property of a table |
| `MAP-028` | a base class used as an entity | declare it `abstract`; never put `[Table]` on a base |
| `MAP-029` | an invalid trigger declaration or link | see [Triggers](10-triggers.md#pitfalls) |
| `MAP-030` / `MAP-031` | no conversion for a type, or a value the rule refuses | register an `ITypeHandler<T>`; fix the data |
| `PRM-001` / `PRM-002` | placeholder without a property, property without a placeholder | match the names |
| `PRM-010` / `PRM-011` / `PRM-012` | a `[Statement]`'s declared parameters and SQL disagree, or the args type differs | align the declaration, the SQL, and the args record |
| `QRY-001` / `QRY-002` | zero or more than one row for a single-row read | use the OrDefault form, or tighten the query |
| `QRY-003` | an embedded SQL resource is missing | fix the path; mark the file as an embedded resource |
| `QRY-004` / `QRY-005` | the statement API on a non-statement, or criteria on a statement | use the other API |
| `QRY-006` | an unknown, unmapped, or ambiguous property in criteria | use `nameof(...)` |
| `QRY-007` | an ordered comparison or `Like` with null, or a null in an `In` list | use `IsNull`/`IsNotNull` |
| `QRY-008` | a negative limit or offset | fix the arithmetic |
| `REL-001` | `LoadAsync` names a property that is not a navigation | spell the declared name |
| `REL-002` | a one-to-one matched more than one row | add the unique index on the target FK |
| `REL-003` | a navigation's shape disagrees with the maps at load time | fix the FK declaration |
| `REL-004` | an unloaded collection was read | `Include` it or `LoadEachAsync` |
| `REL-005` / `REL-006` | join mode with a paged collection, or with two collections | `MultiQuery` or `SubSelect` |
| `CRUD-001` | no row for the key, or an unversioned update/delete hit zero rows | the row is gone |
| `CRUD-002` | wrong key shape | pass the tuple in key order |
| `CRUD-003` | a write against a view or statement | write to the table entity |
| `CRUD-004` | a navigation disagrees with its FK | set the FK property |
| `CRUD-005` / `CRUD-006` / `CRUD-007` | a bad column list for `UpdateOnlyAsync` | property names; no key/version/generated; no empties or repeats |
| `CRUD-010` | optimistic concurrency conflict | reload, reapply, retry |
| `CRUD-012` | a by-key soft delete on a versioned entity | pass the entity |
| `TX-001` | `BeginAsync` while a transaction is active | one transaction per session |
| `TX-002` | a cached plan became stale inside a transaction (Go PostgreSQL) | roll back and rerun, or open a new session |
| `VAL-001` | registered SQL does not prepare | fix the SQL |
| `VAL-010` / `VAL-011` | nullability or declared type disagrees with the member | nullable member or `-- notnull:`; STRICT tables; a handler |
| `VAL-012` / `VAL-013` | a mapped table or column does not exist | migrate, or fix the name |
| `VAL-020` | a non-UTC or markerless datetime, in data or in a clock | store UTC; fix the clock |
| `VAL-021` | `select *` in registered SQL | list the columns |
| `MIG-001` .. `MIG-004` | naming or composition errors in the migrations tree | the message names the class |
| `MIG-010` | an applied step's checksum changed | never edit applied migrations |
| `MIG-011` | the database knows a version the code does not | run matching code |
| `MIG-012` | a view or trigger was changed by hand | review, then `migrate --force` |
| `MIG-013` | a `[Draft]` is pending | review and remove the attribute |
| `MIG-020` | a rollback cannot be derived | snapshots, or `Down()` |
| `MIG-021` | a migration statement failed | the message names the SQL |
| `MIG-030` | pending migrations at validation | run the CLI |
| `DDL-001` / `DDL-002` | DDL for the wrong source kind, or a materialized view where unsupported | pick the right call or dialect |
| `DDL-003` / `DDL-004` | a destructive or inexpressible change in `diff`/sync | `--allow-remove`/`--allow-delete`, or hand-write it |
| `DDL-005` | a trigger form the dialect lacks | no `Before` on SQL Server; `InsteadOf` on views only elsewhere |
| `UOW-001` .. `UOW-006` | tracking and unit-of-work misuse | see [Tracking](07-tracking-and-unit-of-work.md#the-uow--codes) |

## Loader errors are collected

A loader never stops at the first problem: `MappingException.Errors` lists
every violation for the type, and SchemaGuard lists every violation across
every source. Read the whole list before fixing anything; several entries
often share one cause.

## Pitfalls

- Catching `SimpleOrmException` does not catch `ConcurrencyException`,
  `MappingException`, or `SchemaValidationException`; they are separate types.
- Asserting on messages in tests ties you to one implementation's wording.
  Assert on `Code`.
- A provider exception (unique constraint, foreign key) is the provider's own
  type; the library does not translate it.
- `MAP-023` exists only where declarations are not type-checked (Go struct
  tags, PHP token lists): a malformed tag, an unknown option.
