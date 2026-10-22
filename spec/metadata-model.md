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
  nullability, key/generated flags, and the optional storage facets (`Length`
  for strings and bytes, `Precision`/`Scale` for decimals; ADR-0043) each
  dialect renders its own way.
- **Indexes** (tables and materialized views only): name, ordered columns with
  per-column direction, unique flag. Consumed by generated DDL, snapshots, and
  the diff generator (ADR-0011/0017); matched structurally, never by name.
- **Relationships**: many-to-one (FK columns + referenced entity), one-to-one
  and one-to-many (the target's FK columns), many-to-many (the link and its FK
  columns to each side). Declarations only — loading is explicit or eager
  (`loading.md`), never implicit.
- **Triggers** (tables, and views for INSTEAD OF; Level 4, ADR-0042): name,
  timing, events, an optional update-of column list, and one neutral body.
  Declarations only — migrations create them (`migrations.md` "Triggers"); the
  session never reads or fires them.

## Capability rules per source

| | writes | key | generated/version | index | trigger |
|---|---|---|---|---|---|
| table | yes | required | allowed | allowed | allowed (any timing) |
| view | no | allowed | no | no | INSTEAD OF only |
| materialized view | no | allowed | no | **allowed** | no |
| statement | no | no | no | no | no |
| procedure | no | no | no | no | no |

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
order), then each base class upward — a language whose own reflection lists base
members first (a Python dataclass, a Go embedded struct) reorders to match.
Composite key order is `[Key]` declaration order.

**A base class is never an entity** (ADR-0041). A base class shares members only:
its column, key, generated, version, hook, and rename declarations are inherited by
every concrete subclass as if declared there, each concrete relation carrying its
own copy of every column; identity comes from the concrete class alone. A base
class is never read, written, queried, exported, validated, or targeted by a
navigation — a read through one throws `MAP-028` rather than silently excluding or
remapping it. Three cases, all `MAP-028`: an **abstract class** loaded as an entity
by any path that loads a map (a registry result type, a key read, a criteria query,
a write, an explicit or eager load of a navigation typed to it, a manual-builder
registration, the tracker, SchemaGuard, `export-metadata`); a mapped type whose
**base class carries a relation source** (`[Table]`/`[View]`/`[MaterializedView]`/
`[Statement]`/`[Procedure]`) or an `[Index]` of its own — the error names both
classes, and class-level declarations are read **without inheritance**, so a
subclass never adopts its base's relation; and, in an **assembly scan** (SchemaGuard,
the CLI's `export-metadata`/`validate`), a **concrete class without a source that
another scanned type extends** — the one case a lone load cannot see, because such a
class legitimately maps to a convention-named table on its own. Declaring base
classes `abstract` is the convention (the fixture's `BaseModel`). Mapped
inheritance — single-table, table-per-type, a polymorphic read over concrete
tables — is ruled out permanently; a language without subclassing (Go, Rust) has
nothing to refuse in these three forms and records the code as unreachable —
except what its own inheritance-like mechanism would silently carry (Go method
promotion of an embedded struct's entity descriptor), which it refuses with the
same code (see Clarifications).

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

## Triggers (Level 4, ADR-0042)

A trigger is **its own declaration class**, the way a materialized view is a
class carrying its SQL: the class declares the trigger name, the timing, the
events, and **one** body; the entity **links** the class. Nothing on the entity
and nothing in the body names a dialect — a hundredth database type adds one
dialect that knows how to render a trigger, and no declaration changes.

```csharp
[Trigger("trg_roles_audit", TriggerTiming.After, TriggerEvent.Update, """
    insert into role_changes (role_id, role_name, changed_at)
    select n.id, n.role_name, {now}
    from {new: id, role_name} n;
    """)]
public sealed class RoleAuditTrigger { }

[Table("roles")]
[Trigger(typeof(RoleAuditTrigger))]
public sealed class Role { … }
```

- **The declaration** (on the trigger class, exactly one): a name (unique across
  the model — database-wide on SQLite and SQL Server), a timing — `before`,
  `after`, `instead_of` — one or more events — `insert`, `update`, `delete` —
  an optional `updateOf` list naming **properties** of the entity (resolved to
  columns; only with the update event), and the body. The class is a
  declaration object, never an entity: no columns, no key; scans skip it; a
  load of it is `MAP-029`.
- **The link** (on the entity, repeatable): a table carries many triggers; a
  trigger belongs to one table. A table takes every timing, a view only
  `instead_of`, no other source takes a trigger. The entity's map carries the
  resolved triggers in link order.
- **The body** is set-based SQL over the changed rows, each statement ended by
  `;`, real SQL first-class — with three **tokens**, the only non-SQL in it,
  standing for exactly what differs per database:

  | Token | Meaning | SQLite, PostgreSQL | SQL Server |
  |---|---|---|---|
  | `{new: a, b}` | the inserted or updated rows as a relation | `(select new.a as a, new.b as b)` | `inserted` |
  | `{old: a, b}` | the deleted or pre-update rows | the same over `old.` | `deleted` |
  | `{now}` | UTC now in the §7.9 storage form | `strftime('%Y-%m-%dT%H:%M:%fZ', 'now')` / `now()` | `todatetimeoffset(sysutcdatetime(), 0)` |

  The column list is required (the rendering then depends on the body alone,
  never on the table's current shape), the names are **column** names of the
  entity's relation (matched exactly), and a row token must have rows on
  **every** declared event, since one body serves them all: `{new}` refuses a
  declaration that includes delete, `{old}` one that includes insert.
  Braces inside a single-quoted SQL literal are text, not tokens; any spelling
  of a token (`{ NEW : id ,name }`) is accepted and canonicalized to
  `{new: id, name}` wherever the body is normalized (exports, snapshots,
  diffs); a `--` line comment is refused (whitespace is collapsed in
  snapshots and would fold the rest of the body into it — use `/* … */`). Everything around
  the body — `set nocount on`, `begin/end`, the PostgreSQL function and its
  `return`, the granularity (`for each row` where it exists; SQL Server is
  statement-level) — is the dialect's rendering (`dialects.md` "Triggers"),
  configured once, never written by the author. A form a dialect lacks is
  refused when rendered (`DDL-005`, `errors.md`), not at declaration.
- **`MAP-029`** is every misplaced or malformed declaration: a link on a
  statement, procedure, or materialized view; a link to a class without a
  declaration, with two, or that is itself an entity or an owned type; a blank
  name, no event, or a blank body; `before`/`after` on a view; `updateOf`
  without the update event or naming an unmapped property; an unknown or
  malformed token, a row token without columns or naming an unmapped column, or
  one impossible for the events; a class linked twice by one entity. Collected
  with the entity's other errors, never first-error-only.
- **The scan rule** (SchemaGuard, every CLI command that enumerates entities,
  like `MAP-028`): a trigger class linked by two entities, or two classes
  declaring one name — one `MAP-029` per conflict; a lone load cannot see them.
- The manual builder links a trigger class under the same rules.

**Clarifications** (the PHP and Go port gap lists; ADR-0042):

- **Telling the two attribute shapes apart** is the language's business: C#
  by constructor, PHP by the arguments given (a single class argument is a
  link; an incomplete declaration is `MAP-029`, never read as a link), Go by
  a descriptor method on the trigger type and a typed link in the entity's
  descriptor. A declaration with no timing where the language allows one
  (a Go zero value) is `MAP-029`, never a default.
- **A class carrying a link counts as mapped** (it routes to the annotation
  loader, so the link is never silently ignored); a pure declaration class
  does not, so scans skip it; a class that is both an entity and a
  declaration is `MAP-029`. Where class-level annotations are not inherited,
  a link on a base class is simply invisible; where they are (C#), the link
  is read without inheritance like `[Index]` and a base carrying one is
  `MAP-028`.
- **`updateOf`** names properties (dotted paths for owned members), resolved
  to columns, compared in declared order and exactly; the scan rule compares
  trigger names case-insensitively and enforces model-wide uniqueness on
  every dialect, PostgreSQL's per-table names included.
- **A trigger's name is its object.** Renaming a declaration is a removal
  plus a creation, never a change; a declaration moved to another relation
  (or between a table and a view) is `DDL-004` for the generator — the
  generated guard and drop would target the new relation while the trigger
  lives on the old one — and is written by hand (drop on the old relation,
  create on the new).
- **What the dialect renders from metadata** is the author's body trimmed,
  verbatim; only exports, snapshots, and diffs normalize it. The `--` rule
  above keeps the two equivalent. A body may not contain `$$` (PostgreSQL's
  function quoting); the SQLite `{now}` carries three fractional digits
  (`%f`), which the reader accepts like any marked ISO text.
- **`before`/`after` on a view** is refused by the loader only; a dialect
  asked for it through the data-driven step form renders it and the database
  refuses. `DDL-005` stays the two forms of the renderings table.

**Clarifications** (the uniformity review; ADR-0042):

- **What a token is.** A token is `{…}` with no brace inside, outside a
  single-quoted literal — nothing else is masked (a brace in a `/* … */`
  comment or a quoted identifier is read as a token). A lone `{` or `}` that
  opens or closes no token is text. The kind is case-insensitive (`{NOW}`,
  `{ New : a }`); a row token's columns are trimmed, none may be empty, and a
  repeated column is not an error. A body the dialect is asked to render
  through the data-driven form is parsed by the same rules, and a malformed
  token or a `--` comment there is `MAP-029` at rendering.
- **The scan rule's name set** is every declaration class the scan sees — the
  classes entities link and any declaration class in the scanned list itself,
  linked or not (a class kept for its removal step still names the database
  object). The error for a name conflict names the trigger name; for a class
  linked twice, the class.
- **The SQLite read-back** (the shadow, the conformance deep expect) merges the
  per-event triggers only when they form one declaration — the names the
  rendering gives them, one relation, one timing, one normalized body, no
  event twice; the update-of list comes from the update trigger whatever the
  name order. It folds only the exact expansions — one row variable and every
  alias its column — and leaves any other `(select new.… as …)` as the
  author's SQL. Anything else is a hand-written trigger: skipped with a note.

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
    { "column": "id", "type": "int64", "nullable": false, "key": true, "generated": true },
    { "column": "amount", "type": "decimal", "nullable": false, "precision": 18, "scale": 2 }
  ],
  "indexes": [
    { "name": "ix_t_a", "columns": [{ "column": "a", "direction": "asc" }], "unique": true }
  ],
  "triggers": [
    { "name": "trg_t_audit", "timing": "after", "events": ["update"], "updateOf": ["a"],
      "sql": "insert into t_log (t_id) select n.id from {new: id} n;" }
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

Relationship declarations (ADR-0005/0019) are metadata; loading is a separate,
explicit act (`loading.md`). **Nothing loads implicitly**: a navigation stays
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
- **Cascades are declared, never inferred** (Level 3 M2, unit-of-work.md): a
  `many_to_one`, `one_to_one`, or `one_to_many` navigation may declare what
  it cascades — `persist`, `remove`, or both — and the export carries
  `"cascade": ["persist", "remove"]` **only when declared** (an export
  without cascades is unchanged). A `many_to_many` navigation cannot cascade
  (its link rows are entities the unit of work persists explicitly), and a
  cascade on anything but a navigation is a declaration error: `MAP-025`.
  The export writes `"cascade"` as the last key of the relationship object, in
  the fixed order `persist`, `remove`.
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

### Declared hooks (Level 3 M3, hooks.md)

A column may carry one hook, exported only when declared: `"audit":
"created_at"` or `"audit": "updated_at"` (the session fills or stamps it
with its clock), and `"softDelete": true` (a delete sets it). Declaration
errors are `MAP-026`. The fixture declares both audit roles on every
`BaseModel` table and the soft-delete flag on `roles.is_deleted` (V0011).

### Declared triggers (Level 4, "Triggers" above)

`"triggers"` is written after `"indexes"` and only when declared: per trigger
`name`, `timing` (`before` | `after` | `instead_of`), `events` (a list in the
fixed order `insert`, `update`, `delete`), `updateOf` (column names; only when
declared), and `sql` — the body whitespace-normalized with its tokens
**unexpanded**. The trigger class's name never appears: the export stays
column-centric, so every implementation produces the same file. The fixture
`Role` links `RoleAuditTrigger` (`entities/role.json`); `role_changes` is the
table it writes (`entities/role_change.json`).

### Declared renames (Level 3 M5, migrations.md "Draft migrations")

A column may declare the column it was previously stored under, exported
only when declared: `"renamedFrom": "name"`. The diff generator reads it as
a declared rename; the mapper and the session ignore it. `MAP-027` when
declared without `[Column]` or without a name.

### Storage facets (Level 4, ADR-0043; dialects.md "Storage types")

A column may declare a **size** the dialect's default storage type does not
carry — dialect-neutral facets the dialect renders its own way, so the model
can say `decimal(18,2)` without naming a database:

- `Length` on a string or bytes column (`[Column(Length = 50)]`; PHP
  `#[Column(length: 50)]`; Go `orm:"column,length=50"`): SQL Server
  `nvarchar(50)` / `varbinary(50)`, PostgreSQL `character varying(50)`
  (`bytea` has no length), SQLite ignores it. A declared length wins over SQL
  Server's indexable default (450) for a key or indexed column.
- `Precision` and `Scale` on a decimal column (`[Column(Precision = 18, Scale = 2)]`;
  a `Scale` left out is 0): SQL Server `decimal(18,2)`, PostgreSQL
  `numeric(18,2)`, SQLite ignores them.

Exported only when declared, after `renamedFrom`: `"length": 50`, or
`"precision": 18, "scale": 2` (the scale always beside its precision). The
fixture declares `Transaction.Amount` at 18, 2 (`entities/transaction.json`,
`snapshot-cases/transactions_v3`). The mapper and the session ignore the
facets; `StorageType`, snapshots, the diff generator, and force sync read
them. `MAP-032` for a misplaced one: `Length` off a string or bytes column,
`Precision`/`Scale` off a decimal, a `Scale` without a `Precision` or above
it, a non-positive value, or any facet on a non-table source. Whether a
declared size fits an engine's limits is the author's business. Without the
facets a real tree's `decimal(18,2)` would read as a type change against the
model's `decimal(38,9)` on every `diff` — the reason they exist.

**Clarifications** (the PHP and Go ports asked; ADR-0043):

- **"Non-positive"**: a `Length` or `Precision` below 1 refuses; a negative
  `Scale` refuses; an explicit `Scale` of 0 is legal (a left-out scale is 0);
  a `Scale` declared without a `Precision` refuses, even 0, wherever the
  language can tell a declared 0 from an absent one (a C# attribute cannot:
  a bare `Scale = 0` is indistinguishable from nothing and passes).
- **A facet on an owned type's `[Column]` member is legal**: it sizes a column
  of the owner's table, and the dotted property carries it.
- **`Length` is legal on the `string` and `bytes` tokens only**: `enum_text`,
  `guid`, JSON, and handler-typed columns refuse, however they are stored.
- **Where a facet can sit at all is the language's.** In C# it rides on
  `[Column]` and cannot be misplaced; a port whose declaration form allows it
  beside an ignore marker, on a navigation or owned navigation, or without the
  column marker refuses those with `MAP-032`; a malformed value in an untyped
  declaration (Go's tag) is `MAP-023`.
- **A `Precision` with the scale omitted renders `(p,0)`** — what the
  introspection spellings report.

## Clarifications (the PHP and Go ports asked; ADR-0041 add.1)

- **"Abstract" includes interfaces** and any other type that can never be
  instantiated: a map is never loaded for it, whatever the loader.
- **Where the check runs.** The abstract check runs once, first, at the single
  map-loading entry point — before explicit registrations, the annotation loader,
  and the convention loader — so every path that loads a map inherits it. A
  manual-builder registration of an abstract type is refused where it is written
  (at registration), not only when the map is first needed.
- **A navigation typed to an abstract class.** The owner's declaration is
  shape-valid and the owner's map loads; what refuses is every load of the
  target — an explicit load, a batch load, an eager load in any fetch mode, and an
  export that needs the target's map (a one-to-many's foreign-key columns). An
  owner's map that never needs the target's map (a many-to-one) exports.
- **The parent rule applies under every loader** — annotation, convention, and
  manual builder: "a mapped type" is mapped by any of them. It is collected
  beside the type's other errors (never first-error-only), one error per ancestor
  that carries a relation source or an `[Index]`, naming the subclass, that
  ancestor, and the declarations found, however far up the chain — a
  grandparent's `[Table]` is named even when the parent is clean, and an
  abstract ancestor carrying `[Table]` counts: its source names it an entity.
- **"Read without inheritance" is vacuous** where the language's reflection never
  inherits class-level annotations (PHP `getAttributes()`); it is an instruction
  where it does (C# attribute inheritance) or where an inheritance-like mechanism
  exists (Go method promotion: an embedded struct whose entity descriptor names a
  relation source or indexes would be promoted onto the embedding struct — the
  loader refuses the pair with `MAP-028` naming both types, the deepest declaring
  struct through nested embedding, and never reads the promoted descriptor; an
  embedded struct with no descriptor, or one declaring only foreign keys or links,
  shares its fields and nothing else).
- **The scan rule's sets.** A *base candidate* is a concrete, mapped, non-owned
  class carrying no relation source of its own; an *extender* is any scanned
  mapped, non-owned class below it — abstract ones included — directly or through
  intermediates. Every candidate in the scanned list that an extender sits under
  is reported, each once, naming its extenders (sorted by name): a chain
  `Base ← Middle (no source) ← Leaf ([Table])` reports both `Base` and `Middle`.
  An ancestor outside the scanned list is invisible to the scan. A reported base
  is not validated as a relation; its extenders still are. Alone, the same
  concrete class loads and maps to its convention-named table, and so does a
  subclass of it that carries its own `[Table]`.
- **What the scan refuses.** SchemaGuard reports the bases in its complete
  report. Every CLI command that enumerates entities — `export-metadata`,
  `snapshot`, `diff`, `migrate --force`'s sync, `shadow` — refuses before doing
  anything, with exit code 1 and every `MAP-028`; `validate` reports through
  SchemaGuard. Where there is no assembly to scan (PHP), the scan runs over the
  class list the caller passes; where registration is explicit and nothing
  extends anything (Go), the rule has nothing to see.
- **A registry entry whose result type fails to load** (`MAP-028`, or any other
  loader failure) is one SchemaGuard report row per violation, with the entry as
  the source, and the entry's per-column checks are skipped; never an escaping
  exception.
- **The conformance case needs nothing in the fixture database**: the refusal
  happens when the result type's map is first needed, before any row is read.
  Where the code is unreachable, the runner resolves `BaseModel` to its
  field-sharing fixture type and pins the premise (the type is embedded by value
  in the fixture entities) instead of running the query — "passes by
  construction" is honest only because the premise is checked.
