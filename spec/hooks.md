# Declared hooks — audit columns and soft delete (Level 3 M3, ADR-0034 §7c)

Hooks are **declared metadata the session acts on**, never registered
callbacks: a declaration on a column says what the library writes there and
when. Three exist. They are read by the same loaders as everything else, live
in the `EntityMap` (metadata-model.md: `"audit"`, `"softDelete"` on the
column, exported only when declared), and apply to every write path — the
immediate operations and the unit of work alike, because every statement is
the same generated CRUD.

## The session clock

`DbOptions.Clock` (PHP `clock`, Go `Options.Clock`) is the instant the hooks
write — a function returning **UTC** (§7.9; a non-UTC instant is `VAL-020`),
the wall clock by default, fixed by tests and by the conformance runners at
`2026-08-29T00:00:00Z`.

## `[CreatedAt]` — filled on insert when unset

On `Insert` (immediate, or flushed), the column takes the clock **when its
value is unset** — null, or the type's default instant — and keeps a value
the caller set. Nothing else touches it. A `DateTime` or `DateTimeOffset`
column, nullable or not.

## `[UpdatedAt]` — stamped on every write that changes a row

The library is the column's writer. The clock is written on a full-row
`Update`, on a column-list `UpdateOnly` (the column **joins the SET** even
when not listed), on a tracked instance's dirty update (immediate or
flushed), and on a soft delete. It is **never** what makes a tracked
instance dirty: a clean instance still writes nothing and is not stamped.
It is not written on insert — null until the first update, when nullable.
The column-list update's recorded statement therefore lists the stamp
column after the caller's columns (`conformance/tracking-cases`,
`flush-cases`: `["Name", "UpdatedAtUtc"]`).

## `[SoftDelete]` — a delete that sets a flag

On an entity declaring the flag (a non-nullable `bool` column), every
delete becomes **the column-list update that sets the flag true**, with the
`[UpdatedAt]` stamp and the version rules of an update:

- the entity form of `Delete` (and a flushed `Remove`) is version-checked
  like any update (`CRUD-010`), and the instance is forgotten afterwards
  like any deleted row (its flag and stamp are set on it);
- the by-key form of `Delete` sets the flag by key; on a **versioned**
  entity it refuses (`CRUD-012`) rather than bump a version it does not
  know — pass the entity;
- a zero-row result is `CRUD-001`, as for a hard delete.

**Reads never filter on the flag.** A key read returns the flagged row; a
criteria query returns it unless the caller writes
`Criteria.Eq("IsDeleted", false)`; loading, eager loading, and the identity
map see it like any row. There is no hidden WHERE (§2) — the flag is data
the caller reasons about, and the library's only contribution is the write.

## Declaration rules — `MAP-026`

- `[CreatedAt]`/`[UpdatedAt]`: a `DateTime` or `DateTimeOffset` property
  (nullable or not); `[SoftDelete]`: a non-nullable `bool`.
- Each carries `[Column]`; none may be a key, version, or generated column
  (the library writes them as data) and a property carries at most one of
  the three.
- At most one property per role per entity.

The fixture: `BaseModel.CreatedAtUtc` is `[CreatedAt]`, `UpdatedAtUtc` is
`[UpdatedAt]` (every sample table), and `Role.IsDeleted` is `[SoftDelete]`
(migration V0011).

## Conformance: `hook-cases/`

The step vocabulary of `tracking-cases`/`flush-cases` (session.md,
unit-of-work.md), run with the clock fixed; the runner observes the
statements through the dialect seam. Expectations on the audit columns use
`value` with the fixed clock; a soft delete shows as an `update` statement
whose columns are the flag and the stamp.

## Clarifications (the PHP and Go M3 ports asked; ADR-0037)

- **"UTC" is the language's notion of it**: a `DateTimeKind.Utc` instant in C#,
  a `DateTimeImmutable` whose zone is `UTC`/`Z`/`+00:00` in PHP, a zero-offset
  `time.Time` in Go. Any
  other instant from the clock is `VAL-020` at the write that would use it.
- **The clock is read per write** — once per statement that fills or stamps —
  so a multi-row flush may stamp different instants; the conformance runners
  fix it, so cases never depend on that.
- **A failed immediate write leaves the instance as it was**: the stamp (and a
  soft delete's flag) go back to their previous values when the statement
  fails (`CRUD-010`, a provider error). A flush's undo journal does the same
  for every statement it ran — the stamp of an undone update, the flag,
  stamp, and version of an undone soft delete, and the fill of an undone
  insert's `[CreatedAt]`.
- **Hooks run after the gates**: the clock is consulted after the list
  validation, the navigation consistency check, and the identity gate, so
  `CRUD-005`/`CRUD-004`/`UOW-002` outrank a `VAL-020` from the clock.
- **Listing the stamp column in a column-list update** changes nothing: it is
  stamped with the clock and written once.
- **A by-key soft delete** sets nothing on any instance (there is none); it
  forgets the tracked instance for that key. `CRUD-012` is decided before the
  key validates, so it outranks `CRUD-002` on a versioned entity.
- **`[CreatedAt]` "unset"** means null, the type's zero instant, or (in a
  language where the property may be uninitialized) not initialized.
- **Hooks are only valid on a table-backed entity** (`MAP-026` on a view,
  statement, or procedure — a hook that could never act is a declaration
  error, as `[Generated]`/`[Version]` are there, `MAP-013`). The manual map builder declares hooks like the
  attribute loader does, and the same `MAP-026` rules apply to both.
- **`MAP-026` covers every misplaced hook**: on a navigation, an ignored or
  unmapped property, an owned navigation or an owned type's member — never
  `MAP-019`/`MAP-024` for those. A duplicate hook token on one declaration
  (Go `created_at,created_at`) is the port's own malformed-declaration rule.
- **A null/nil entity** passed to a hook-bearing write, `Detach`, or `Merge`
  is the language's argument error where the language has one (C#
  `ArgumentNullException`, PHP `TypeError`); Go, which cannot refuse a nil
  pointer by type, reports `UOW-001` for `Detach` and `CRUD-002` for `Merge`.
- **A copy's uninitialized property** (PHP: a typed property never assigned)
  is not merged — there is no value to copy; everything else is copied as is.
- **Refusal order on a by-key soft delete**: `CRUD-012` (a versioned entity)
  is decided before the key validates — the call can never succeed there.
- **A zero database-generated key is unset** everywhere it matters (merge,
  the identity map's null-part rule aside): an explicit `0` on a generated
  key is not a key value.
