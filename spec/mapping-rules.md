# Mapping rules — construction, conversion, strictness

How a result row becomes an object, and how CLR values cross the database boundary.
One pipeline serves raw SQL, statement entities, and generated reads (§7.11);
implementations may compile mappers per (result type, column set), but behavior —
including every error code — must match this document.

## Strictness

Nothing is silent. For an **entity result** (a type with mapping metadata), the
result columns and the mapped properties must match **exactly, both ways**:

- a result column with no mapped property → `MAP-001`
- a mapped property with no result column → `MAP-002`

Partial selections of an entity are expressed as DTOs, not as half-filled entities.
Strictness applies even to empty result sets: the shape is validated from the
result schema before the first row.

## Construction (the §7.8 algorithm)

Column targets resolve first: for entities, column name → mapped property (via
metadata, case-insensitive); for DTOs, column ↔ member matching is case- and
underscore-insensitive (`created_at` matches `CreatedAt`).

1. A public constructor is a **candidate** when every parameter name matches a
   column's target (case-insensitive).
2. Among candidates, the highest parameter count wins. Two candidates at the same
   highest count → `MAP-003`.
3. No candidate → the parameterless constructor is used; none → `MAP-003`.
4. Columns not consumed by the constructor assign through settable properties; a
   leftover column with no settable target → `MAP-001`.
5. DTO members declared *required* (C# `required`; other languages use their
   equivalent or treat all constructor parameters as required) that end up unbound
   → `MAP-002`.

## Owned types (ADR-0030)

An owned type's members (`metadata-model.md`, "Owned types") are ordinary
columns of the owner for every strictness rule above: a missing owned column is
`MAP-002`, an unexpected one `MAP-001`. What differs is construction:

- **Reading.** The owner constructs as usual (its constructor algorithm never
  sees owned members — their dotted names match no parameter). Then, per owned
  navigation: when the navigation is **nullable** and every one of its member
  columns is NULL in the row, the navigation stays null; otherwise the owned
  type is constructed through its parameterless constructor, its members
  assigned from their columns (a NULL into a non-nullable member is `MAP-031`,
  exactly as for an entity), and the instance attached. A **required**
  navigation is always constructed.
- **Writing.** Insert and the full-row update bind every owned column; a null
  nullable navigation binds NULL for each member. The column-list update binds
  the listed members, or all of them when the navigation itself is listed.
- **Identity and concurrency** are the owner's: an owned type has no key and
  no version.

`conformance/cases/owned_read.json` pins the read; `conformance/crud-cases/owned_profile.json`
the write paths and the all-NULL rule.

## Conversion

Two mechanisms only — the fixed table below and registered type handlers; handlers
win, and anything else fails with `MAP-030` (no rule) or `MAP-031` (rule failed for
the value). No reflection-based guessing.

| neutral type | database (SQLite) | read accepts | write produces |
|---|---|---|---|
| `int16`/`int32`/`int64` | INTEGER | integer/text/real | integer |
| `decimal` | TEXT | text/integer/real | invariant text |
| `double`/`float` | REAL | real/integer/text | real |
| `bool` | INTEGER | 0/1, integer | 0/1 |
| `string` | TEXT | anything (converted) | text |
| `guid` | TEXT | text, 16-byte blob | text (provider) |
| `bytes` | BLOB | blob | blob |
| `datetime` | TEXT | ISO-8601 **with UTC/offset marker** | ISO-8601 `o`, UTC |
| `datetimeoffset` | TEXT | ISO-8601 with offset | ISO-8601 `o` |
| `date` / `time` | TEXT | ISO-8601 | ISO-8601 |
| `enum_text` | TEXT | name, case-insensitive (`MAP-031` if unknown) | the name |
| `enum_int` | INTEGER | integer or name | the integer (`[EnumAsInt]` columns only; parameters always bind names) |

**The UTC rule (`VAL-020`), final form.** Reading: a stored datetime string must end
with `Z` or an explicit `±hh:mm` offset; anything else is an error, never a guess.
Values with offsets normalize to UTC (`DateTime.Kind == Utc` always). Writing: a
`DateTime` with `Kind == Utc` stores as ISO-8601 `o`; `Local` converts to UTC first;
`Unspecified` is an error.

**Nulls**: database NULL → CLR null; NULL into a non-nullable value type is
`MAP-031`. Nullability of reference types follows the metadata (VAL-010 validates
statically at milestone 6).

## Type handlers and JSON columns

`ITypeHandler<T>` (registered on the session options) converts one CLR type both
directions; it always wins over the fixed table. The built-in JSON handler covers
§7.10 nesting: a TEXT column holding JSON deserializes into any registered type,
with snake_case names, case-insensitive matching, and numbers readable from strings.
The nested-result SQL pattern:

```sql
select t.id, t.amount,
       (select json_group_array(json_object(
                'description', d.description,
                'quantity',    d.quantity,
                'unit_price',  d.unit_price))
        from transaction_details d
        where d.transaction_id = t.id) as details
from transactions t
```

An aggregate over zero child rows yields `[]` — an empty list, never null.

## Conformance value encoding

`conformance/cases/*.json` expected values (and `fixtures/seed.json`) use:

| CLR value | JSON encoding |
|---|---|
| integers, floats | number |
| bool | true/false |
| decimal | **string**, invariant (`"19.99"`) — never a float |
| string | string |
| datetime / datetimeoffset | ISO-8601 `o` string |
| date / time | ISO-8601 string |
| guid | lowercase `D` string |
| bytes | base64 string |
| enum | its name string |
| null | null |

Case files: `{ "name", "result": "<EntityName>"\|"raw", "query", "expect": { "rows": […] } \| { "error": "CODE" } }`.
`result: "raw"` compares provider-level values; an entity name maps the rows through
that entity and encodes its properties. Databases build from entity metadata and
seed from `fixtures/seed.json` before each case.
