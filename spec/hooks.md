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
write — a function returning **UTC** (§7.9), the wall clock by default. Every
Level 3 runner fixes it at `2026-08-29T00:00:00Z` (session.md "The step
scripts"), and tests fix it likewise.

**The clock must read UTC outright**, in the language's notion of it: a
`DateTimeKind.Utc` instant in C#, a `DateTimeImmutable` whose zone is
`UTC`/`Z`/`+00:00` in PHP, a zero-offset `time.Time` in Go (ADR-0037). Any
other instant from the clock is `VAL-020` at the write that would use it;
nothing converts there, because a clock that is not UTC is a configuration
error, not a value (ADR-0039). (A *value* is read differently: an explicit
zero offset is UTC, another explicit offset is a moment that converts to UTC
when stored — C# `DateTimeKind.Local`, an aware `+02:00` — and an instant with
no marker at all — C# `Unspecified`, a naive `datetime` — is `VAL-020`.)

**The clock is read per write** — once per statement that fills or stamps —
so a multi-row flush may stamp different instants; the conformance runners
fix it, so cases never depend on that (ADR-0037). **Hooks run after the
gates**: the clock is consulted after the list validation, the navigation
consistency check, and the identity gate, so `CRUD-005`/`CRUD-004`/`UOW-002`
outrank a `VAL-020` from the clock (ADR-0037).

## `[CreatedAt]` — filled on insert when unset

On `Insert` (immediate, or flushed), the column takes the clock **when its
value is unset** and keeps a value the caller set. Nothing else touches it. A
`DateTime` or `DateTimeOffset` column, nullable or not.

**"Unset"**, port-neutrally: null, absent or uninitialized (in a language
where a property may be uninitialized), or the type's zero value — the zero
instant, a `0` database-generated key (an explicit `0` on a generated key is
not a key value), an empty GUID. The same reading serves `[CreatedAt]`,
merge's "unset key" (unit-of-work.md "Detach and merge"), and the identity
map's null-part rule (ADR-0037, ADR-0039).

## `[UpdatedAt]` — stamped on every write that changes a row

The library is the column's writer. The clock is written on a full-row
`Update`, on a column-list `UpdateOnly` (the column **joins the SET** even
when not listed; listing it changes nothing — it is stamped with the clock and
written once, ADR-0037), on a tracked instance's dirty update (immediate or
flushed), and on a soft delete. It is **never** what makes a tracked
instance dirty: a clean instance still writes nothing and is not stamped
(unit-of-work.md "Order of writes", the two meanings of "dirty").
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
  know — pass the entity. `CRUD-012` is decided before the key validates —
  the call can never succeed there — so it outranks `CRUD-002`. A by-key soft
  delete sets nothing on any instance (there is none); it forgets the tracked
  instance for that key (ADR-0037);
- a zero-row result is `CRUD-001`, as for a hard delete.

Which form a call is follows the argument, not the method's name
(unit-of-work.md "Arguments, I/O, and lifetime": Go's `Delete` /
`DeleteEntity`).

**Reads never filter on the flag.** A key read returns the flagged row; a
criteria query returns it unless the caller writes
`Criteria.Eq("IsDeleted", false)`; loading, eager loading, and the identity
map see it like any row. There is no hidden WHERE (§2) — the flag is data
the caller reasons about, and the library's only contribution is the write.

## A failed write leaves the instance as it was

When an immediate write's statement fails (`CRUD-010`, a provider error), the
stamp, a soft delete's flag, and an insert's `[CreatedAt]` fill go back to
their previous values. A flush's undo journal does the same for every
statement it ran — the stamp of an undone update, the flag, stamp, and version
of an undone soft delete, and the fill of an undone insert's `[CreatedAt]`
(ADR-0037; unit-of-work.md "Transactions").

## Declaration rules — `MAP-026`

- `[CreatedAt]`/`[UpdatedAt]`: a `DateTime` or `DateTimeOffset` property
  (nullable or not); `[SoftDelete]`: a non-nullable `bool`. The type tests
  read the **declared** type — the neutral type token (`datetime`/
  `datetimeoffset` for the audit roles, a non-nullable `bool` for the flag);
  where the language declares no type, the declaration must carry it, and a
  hook on a property whose type the loader cannot know is `MAP-026`
  (ADR-0039).
- Each carries `[Column]`; none may be a key, version, or generated column
  (the library writes them as data) and a property carries at most one of
  the three.
- At most one property per role per entity.
- Hooks are valid only on a **table-backed** entity: on a view, statement, or
  procedure a hook could never act, so it is a declaration error, as
  `[Generated]`/`[Version]` are there (`MAP-013`) (ADR-0037).
- `MAP-026` covers **every** misplaced hook — on a navigation, an ignored or
  unmapped property, an owned navigation or an owned type's member — never
  `MAP-019`/`MAP-024` for those. A duplicate hook token on one declaration
  (Go `created_at,created_at`) is the port's own malformed-declaration rule
  (ADR-0037).
- The manual map builder declares hooks as the attribute loader does, under
  the same rules.

The fixture: `BaseModel.CreatedAtUtc` is `[CreatedAt]`, `UpdatedAtUtc` is
`[UpdatedAt]` (every sample table), and `Role.IsDeleted` is `[SoftDelete]`
(migration V0011).

## Conformance: `hook-cases/`

The step vocabulary of `tracking-cases`/`flush-cases` (session.md,
unit-of-work.md), run with the clock fixed; the runner observes the
statements through the dialect seam. Expectations on the audit columns use
`value` with the fixed clock; a soft delete shows as an `update` statement
whose columns are the flag and the stamp.

## Where the Clarifications went

The two lists were never numbered; by entry title.

| Clarification | → section |
|---|---|
| "UTC" is the language's notion of it (ADR-0037) | "The session clock" |
| The clock is read per write (ADR-0037) | "The session clock" |
| A failed immediate write leaves the instance as it was (ADR-0037) | "A failed write leaves the instance as it was" |
| Hooks run after the gates (ADR-0037) | "The session clock" |
| Listing the stamp column in a column-list update (ADR-0037) | "`[UpdatedAt]` — stamped on every write that changes a row" |
| A by-key soft delete (ADR-0037) | "`[SoftDelete]` — a delete that sets a flag" |
| `[CreatedAt]` "unset" (ADR-0037) | "`[CreatedAt]` — filled on insert when unset" |
| Hooks are only valid on a table-backed entity (ADR-0037) | "Declaration rules — `MAP-026`" |
| `MAP-026` covers every misplaced hook (ADR-0037) | "Declaration rules — `MAP-026`" |
| A null/nil entity (ADR-0037) | unit-of-work.md "Arguments, I/O, and lifetime" |
| A copy's uninitialized property (ADR-0037) | unit-of-work.md "Detach and merge" |
| Refusal order on a by-key soft delete (ADR-0037) | "`[SoftDelete]` — a delete that sets a flag" (merged with "A by-key soft delete") |
| A zero database-generated key is unset (ADR-0037) | "`[CreatedAt]` — filled on insert when unset" |
| "UTC" without a `Kind` (ADR-0039) | "The session clock" |
| `MAP-026`'s type tests read the declared type (ADR-0039) | "Declaration rules — `MAP-026`" |
| "Unset", port-neutrally (ADR-0039) | "`[CreatedAt]` — filled on insert when unset" |
| Every Level 3 runner fixes the clock (ADR-0039) | "The session clock" |
