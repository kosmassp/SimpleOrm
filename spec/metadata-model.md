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
  properties must say so (`[Ignore]`) or be relationship declarations. Navigation
  properties must not be publicly settable (`MAP-011`).
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
    { "kind": "many_to_one", "foreignKeyColumn": "user_id", "references": "User" }
  ]
}
```

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
