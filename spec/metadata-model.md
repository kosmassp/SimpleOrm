# Metadata model — EntityMap

The `EntityMap` is the single source of truth about a mapped type. It is produced
only by loaders; every other subsystem (mapping, CRUD, validation, migrations, the
Level 2 query model) reads it and never the language-level annotations. Each
implementation language declares entities natively (C# attributes, Go struct tags,
Java annotations, PHP attributes) and must produce the identical JSON export
described here.

## Contents

Per entity:

- **Relation source** (exactly one): `table`, `view`, `materialized_view`,
  `statement`, or `procedure`. Every non-table source is self-contained: views and
  materialized views carry their defining SELECT (which takes no parameters);
  statements carry their SQL plus a declared parameter list; procedures carry name,
  body SQL, and a declared parameter list. Named sources also carry an optional
  schema.
- **Key**: ordered key columns and a strategy — `database_generated` (single key the
  database produces; read back on insert), `client_guid` (single GUID key the client
  supplies), `natural` (caller-supplied, incl. composite), `none` (keyless).
- **Version column** (optional, at most one; integer; tables only): optimistic
  concurrency.
- **Columns**: one entry per mapped property — column name, neutral type token,
  nullability, key/generated flags.
- **Indexes** (tables and materialized views only): name, ordered columns with
  per-column direction, unique flag. Declaration-only until Level 3 migrations.
- **Relationships** (declaration-only until Level 2): many-to-one entries — FK
  column and referenced entity.

## Capability rules per source

| | writes | key | generated/version | index |
|---|---|---|---|---|
| table | yes | required | allowed | allowed |
| view | no | allowed | no | no |
| materialized view | no | allowed | no | **allowed** |
| statement | no | no | no | no |
| procedure | no | no | no | no |

Violations are loader errors with the `MAP-`/`PRM-` codes in [errors.md](errors.md).
Every violation for a type is collected before failing — never first-error-only.

## Loader precedence

Explicit (manual builder registration) → annotation loader → convention loader.

- **Annotation loader**: used when the type carries any mapping annotation. Mapping
  is opt-in: a property is mapped iff explicitly marked (C#: `[Column]`); a public
  settable property with no marking at all is an error (`MAP-010`); non-column
  properties must say so (`[Ignore]`) or be relationship declarations. The
  library is a navigation's only writer (`MAP-011`, an intent: enforced where
  the language can express it, documented where it cannot — ADR-0029).
- **Convention loader**: used when the type has no mapping annotations. Every public
  settable property maps by convention; a property named `Id` is the key
  (database-generated for integer types, client GUID for GUIDs); the type maps to a
  table named by the convention.
- **Manual builder**: maps only the properties explicitly configured; same
  validations as the annotation loader.

Inherited properties are mapped. Property order — and therefore column order in the
export — is: the most-derived class's declared properties first (in declaration
order), then each base class upward. Composite key order is `[Key]` declaration
order.

## Naming convention

Wherever a name is derived rather than explicit, a configurable naming convention
translates language-side names to database names. An explicit name always bypasses
it. The default is snake_case; its algorithm: insert `_` before an upper-case letter
that follows a lower-case letter or digit, or that starts the last word of an
acronym run; lower-case everything. Normative vectors (every implementation must
match):

| input | output |
|---|---|
| `Name` | `name` |
| `UserId` | `user_id` |
| `UserID` | `user_id` |
| `APIKey` | `api_key` |
| `HTMLParser` | `html_parser` |
| `Address2` | `address2` |
| `Address2B` | `address2_b` |
| `CreatedAtUtc` | `created_at_utc` |
| `ID` | `id` |
| `camelCase` | `camel_case` |
| `already_snake` | `already_snake` |

Derived table names are the snake_case type name, **never pluralized**. Derived
index names are `ix_<table>_<column>[_<column>…]`.

## Owned types (ADR-0030)

An **owned type** is a value object stored as columns of its owner's table: it
has no table, key, version, generated column, relationship, index, or nested
owned type of its own, and it is never an entity — assembly scans (the CLI,
SchemaGuard, snapshot tooling) skip it. Two declarations, both required: the
**type** is marked owned at class level (that is what keeps it out of the entity
set), and the owner's **navigation property** is marked owned too (mapping stays
opt-in per property; the navigation may set a column prefix).

```csharp
[Owned] public sealed class Address { [Column] public string Street { get; set; } … }
[Table("user_profiles")] public sealed class UserProfile { …  [Owned] public Address? Address { get; set; } }
```

Rules:

- The owned type's members follow the entity rules for `[Column]`, `[Ignore]`,
  `[EnumAsInt]`, and the opt-in check (`MAP-010`); anything else on a member —
  key, version, generated, foreign key, relationship, nested owned — is
  `MAP-024`. So are: a navigation that is a collection or a scalar, a
  navigation without a setter, an owned type that is an entity, that lacks the
  class-level declaration, that has no parameterless constructor, or that maps
  no columns; and loading an owned type as if it were an entity.
- **Flattening.** Each member becomes an ordinary column of the owner:
  `<prefix><member column>`, where the member column follows the usual rule
  (bare `[Column]` → convention name; explicit name kept) and the prefix
  defaults to the convention name of the navigation plus `_` (`Address` →
  `address_`), overridable per navigation (including to the empty string). A
  prefixed column colliding with another column is `MAP-018`. The members sit
  in the owner's property list at the navigation's declaration position, in
  the owned type's declaration order. **The `EntityMap` is therefore flat**:
  every subsystem — CRUD, criteria, DDL, snapshots, diff, validation, the
  export — sees only columns; only the mapper regroups them.
- **Naming.** A member's property name is the dotted path `Navigation.Member`
  (`Address.City`) — the name criteria, orderings, indexes, and column lists
  use, resolved like any other property (`QRY-006` when unknown). An update by
  column list also accepts the navigation's own name, which stands for all its
  members.
- **Nullability.** A nullable navigation makes every member column nullable,
  whatever the member's own type: a null value stores as all-NULL. A required
  (non-nullable) navigation keeps each member's own nullability.
- **Export.** Column-centric like everything else: the flattened columns
  appear as ordinary columns and nothing records the owned structure — two
  implementations that flatten the same declaration produce identical exports.

Reading and writing rules are in `mapping-rules.md`; the fixture is
`UserProfile.Address` (`address_street`, `address_city`, `address_postal_code`).

## Entity identity

An entity's identity is its ordered key values, extracted from an instance.
Two instances are identity-equal iff all key values are equal, position by
position. Keyless entities (statements, procedures, keyless views) have no
identity; asking for it is an error. Level 2 graph reshaping and the Level 3
identity map build on exactly this definition.

## JSON export

The conformance artifact (`conformance/entities/*.json`), one file per entity,
snake_case file names. The export is **column-centric and language-neutral**:
column names, neutral type tokens, and SQL-side parameter names only — never
language-side property names, which legitimately differ per implementation
(C# `UserId`, Go `UserID`, Java `userId`).

Shape (fields omitted when empty/false; two-space indent; statement SQL is
whitespace-normalized so formatting differences across languages don't break
byte-equality):

```json
{
  "entity": "Transaction",
  "source": { "kind": "table", "name": "transactions" },
  "key": { "strategy": "database_generated", "columns": ["id"] },
  "version": "version",
  "columns": [
    { "column": "id", "type": "int64", "nullable": false, "key": true, "generated": true }
  ],
  "indexes": [
    { "name": "ix_t_a", "columns": [{ "column": "a", "direction": "asc" }], "unique": true }
  ],
  "relationships": [
    { "kind": "many_to_one", "foreignKeyColumns": ["user_id"], "references": "User" },
    { "kind": "one_to_one", "references": "UserProfile", "targetForeignKeyColumns": ["user_id"] },
    { "kind": "one_to_many", "references": "Transaction", "targetForeignKeyColumns": ["user_id"] },
    { "kind": "many_to_many", "references": "Role", "through": "UserRole",
      "linkForeignKeyColumnsToOwner": ["user_id"], "linkForeignKeyColumnsToTarget": ["role_id"] }
  ]
}
```

Relationship declarations (ADR-0005/0019) are metadata only until Level 2
milestone 3 loading. **Nothing loads implicitly**: a navigation stays
empty/null until requested — explicitly, or eagerly with the query — and never
loads on access (ADR-0019 add.1). The four classic cardinalities are the whole
taxonomy: polymorphic relations and "through" traversals are ruled out
permanently (no foreign-key integrity / plain SQL says it better). Rules:

- A navigation is transient — never a column, never written — and **the library
  is its only writer** (`MAP-011`), so it can never disagree with the foreign
  key. The rule is an intent (ADR-0029): where the language can express it the
  loader enforces it (C#: no public setter; PHP: `private(set)`), where it
  cannot (Go: exported fields, which the library must be able to fill) the port
  documents it and `MAP-011` is unreachable.
- **Foreign keys are lists**: one entry per part of the referenced side's key,
  **in that key's order** — composite keys declare several (ADR-0019 add.1), and
  the count must match the key's arity wherever the key shape is declared.
- `many_to_one`: the FK properties are mapped properties of this entity
  (`MAP-016` when unknown, or on an arity mismatch with the target key); the
  export carries their **column** names.
- `one_to_one` / `one_to_many`: the FK lives on the target entity; the
  declaration names it by property in the implementation's own vocabulary, the
  named properties must exist there, and their count must match this entity's
  key arity (`MAP-021`). The export carries their **column** names, resolved
  through the target's map (`targetForeignKeyColumns`, ADR-0029) — the export
  never carries language-side names. `one_to_many` is a collection — the element type comes from
  the property's `IEnumerable<T>`, anything else is `MAP-020`; `one_to_one` is a
  single entity reference — a collection is `MAP-020`. True 1:1 integrity is the
  database's (a unique index on the target FK).
- `many_to_many`: the link entity is **declared, never inferred**; its
  `[ForeignKey]` declarations identify which link properties reference each
  side, in declaration order, and each side's count must match that side's key
  arity (`MAP-022` — missing, surplus, or short). The export carries the link's
  **column** names for both sides (`linkForeignKeyColumnsToOwner`,
  `linkForeignKeyColumnsToTarget`), resolved through the link's map.
- A property carries at most one relationship declaration, and none of
  `[Column]`/`[Ignore]` beside it (`MAP-019`).

A statement source instead carries
`{ "kind": "statement", "sql": "…", "parameters": [{ "name": "since", "type": "datetime" }] }`;
view and materialized-view sources add `"sql"` (the defining SELECT, whitespace-
normalized) to their name; a procedure source carries name, `"sql"`, and
`"parameters"`.

### Neutral type tokens

`int16` `int32` `int64` `decimal` `double` `float` `bool` `string` `guid` `bytes`
`datetime` `datetimeoffset` `date` `time` `enum_text` `enum_int`. Nullability is
carried separately, never in the token. Types outside the vocabulary export as
`clr:<full name>` (implementation-specific; requires a registered handler and is
not portable — conformance entities must not use them).
