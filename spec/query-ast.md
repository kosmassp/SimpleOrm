# Query AST — criteria as data, rendered by the dialect

The criteria query is an **AST, never SQL text** (§10.4, ADR-0012/0020). Every
front-end — the string-based criteria chain today, the Level 2 fluent front-end
later — produces the same tree; the **dialect** turns the tree into SQL. Criteria
name **properties, not columns** (`QRY-006` when unknown); every value binds as a
parameter — identifiers come from the metadata and values from the parameter
binder, so no SQL is ever built from user data. Composition is explicit trees, so
SQL's and/or precedence ambiguity cannot occur.

Criteria queries need a named relation: tables and views. A statement- or
procedure-backed entity refuses with `QRY-005` (statements execute via the
statement API). That gate lives in the session, before any rendering, so
`conformance/ast/` cannot express it — implementations pin it with their session
tests.

## Node vocabulary

| op | SQL token | fields | meaning |
|---|---|---|---|
| `eq`, `ne` | `=`, `<>` | `property`, `value` (nullable) | equality / inequality; a null value renders `is [not] null` |
| `gt`, `ge`, `lt`, `le` | `>`, `>=`, `<`, `<=` | `property`, `value` | ordered comparison; null is `QRY-007` |
| `like` | `like` | `property`, `value` (string) | SQL LIKE; the caller supplies the wildcards (`%`, `_`); null is `QRY-007` |
| `in` | `in (…)` | `property`, `values` (array) | membership; empty renders the false predicate; a null element is `QRY-007` |
| `is_null`, `is_not_null` | `is [not] null` | `property` | explicit null check |
| `and`, `or` | `and`, `or` | `args` (array of nodes) | composite, parenthesized when rendered; **empty renders its identity truth-value** — `1 = 1` for `and`, `1 = 0` for `or` (dynamic composition legitimately produces empty lists; invalid SQL names nothing) |
| `not` | `not` | `arg` (one node) | negation |

The SQL tokens are part of the contract — `ne` renders `<>`, never `!=` — and
`conformance/ast/comparison_operators.json` pins them exactly.

The `op` names are the JSON encoding used by `conformance/ast/`; each
implementation exposes them natively (C#: `Criteria.Eq/Ne/Gt/Ge/Lt/Le/Like/In/
IsNull/IsNotNull/And/Or/Not` static factories building an opaque tree).

## The select

A select is: the source entity's metadata, a **predicate list** (implicitly
ANDed; empty means no WHERE clause), an ordering list, and optional limit and
offset (64-bit integers; **negative is `QRY-008`** — dialects disagree on what a
negative limit means, and SQLite's answer is "no limit at all", so the arithmetic
bug is refused instead of silently returning everything). JSON encoding:

```json
{ "where": [ { "op": "ge", "property": "CreatedAtUtc", "value": "2026-01-01T00:00:00Z" } ],
  "orderBy": [ { "property": "Name", "order": "desc" }, { "property": "Id" } ],
  "limit": 20, "offset": 5 }
```

`order` is `"asc"` (the default, omittable) or `"desc"`. Every field is optional;
`{ }` is select-all.

The reference chain builds exactly this:
`db.Query<T>().Where(…).OrderBy(prop, order).Limit(n).Offset(n)` — `Where` takes
several criteria at once, and both multiple arguments and repeated calls append
to the one implicitly ANDed list. The terminal forms materialize through the
session's one mapping pipeline; `SingleAsync` throws `QRY-001` on zero rows and
`QRY-002` on more than one, `SingleOrDefaultAsync` returns null / throws
`QRY-002`.

## Property resolution

Every `property` — in predicates and in orderings — is a **property name** of
the source entity, resolved to the column name at render time: an exact-case
match wins outright; otherwise a case-insensitive match applies, but one that
fits more than one property is **ambiguous and refuses** (`QRY-006` names the
candidates) — never a silent first-wins. An unknown or unmapped property is
`QRY-006`, named in the error along with the query (`<Entity> criteria`) — never
silently ignored, never passed through to the database as an identifier.

## Rendering (the reference)

Rendering is pure — AST in, SQL text plus ordered parameter values out; no
database. The reference rendering:

- **Explicit column list, never `*`**: every mapped property's column, in
  metadata order, `from` the relation name.
- One predicate in the WHERE list renders bare; two or more wrap in a single
  `and` composite (so the SQL carries that composite's parentheses).
- Composites parenthesize themselves: `(a and b)`, `(a or b or c)`. Nesting in
  the tree is nesting in the SQL.
- Negation renders `not ` followed by the rendered inner node (an inner
  composite brings its own parentheses).
- Orderings render in declaration order; ascending is unmarked, descending
  appends ` desc`.
- Limit and offset bind as parameters and render through the dialect's
  limit/offset clause. SQLite: `limit @cN`, `limit @cN offset @cM`, and — since
  SQLite requires LIMIT before OFFSET — offset alone renders `limit -1 offset @cN`.

The kitchen sink (`conformance/ast/where_order_paging.json`), rendered for
SQLite:

```
select id, name, email, display_name, created_at, updated_at from users where ((id = @c0 or name in (@c1, @c2)) and created_at >= @c3) order by name desc, id limit @c4 offset @c5
```

## Parameters

Values are **always parameterized** — a value never appears in the SQL text, an
empty IN list included. Placeholders are named `@c0…` in **render order**: the
WHERE predicates depth-first left-to-right, then limit, then offset. The renderer
receives a bind function (value in, placeholder out) and calls it in exactly that
order; the session binds each value onto the command through the conversion table
and handler registry (`spec/mapping-rules.md`) on the way. The bind carries the
**compared property**, so per-column conversion applies — an enum value against
an `[EnumAsInt]` column binds as its number, not its name; paging values bind
with no property.

## Null semantics (ADR-0020)

Explicit and strict — a **null in the criteria tree** never silently invokes
SQL's three-valued logic. (NULL *column data* evaluates by standard SQL rules:
`ne` against a value still excludes rows where the column is NULL — that is
what `is_not_null` composition is for.)

- `eq` with null renders `is null`; `ne` with null renders `is not null` — never
  `= NULL`, which silently matches nothing. `is_null`/`is_not_null` render
  identically and are the self-documenting forms.
- An ordered comparison (`gt`/`ge`/`lt`/`le`) or `like` with null is meaningless
  three-valued SQL: `QRY-007`, with the IsNull/IsNotNull guidance.
- A null element inside an IN list can never match (SQL IN's NULL rule):
  `QRY-007`; the message points to `Or(In(…), IsNull(…))`.
- An **empty** IN list matches no rows, visibly: it renders the false predicate
  `1 = 0` — valid SQL, zero rows, never a syntax error and never all rows.

## The dialect seam

`IDialect` carries one member for the whole contract: render a select AST, given
the bind function (§7.25 grew `SelectSql` in ADR-0020). The core ships the
reference rendering above (`AnsiSelectRenderer`); a dialect normally delegates to
it and overrides only what its SQL disagrees with. The reference consults four
dialect knobs (ADR-0024 — SQL Server was the second dialect that forced them):

- **Limit/offset clause** (`LimitOffsetClause`): SQLite renders `limit`/`offset`,
  SQL Server `offset … rows fetch next … rows only`. Parameters always bind
  limit-first regardless of where the clause places them.
- **Identifier quoting** (`QuoteIdentifier`): every relation and column name the
  renderer emits passes through it. SQLite returns the name unquoted (the
  reference rendering *is* the SQLite rendering, byte for byte); SQL Server
  brackets everything — legacy schemas are full of reserved words.
- **Paging needs ORDER BY** (`PagingRequiresOrderBy`): where true (SQL Server), a
  paged select with no orderings gains the constant placeholder
  `order by (select null)` — an unordered page was order-arbitrary anyway.
- **Row-value IN** (`SupportsRowValueIn`): where false (SQL Server), a composite
  subquery membership `(a, b) in (select …)` rewrites as a correlated EXISTS over
  the same subquery — the root aliases as `t`, the subquery becomes derived table
  `s`, and each compared column correlates `s.<projected> = t.<property>`.
  Identical rows match; parameters keep their order (the subquery binds inline).

This keeps a second dialect's rendering cost near zero while preserving the rule
that matters: **front-ends never emit SQL text; only the dialect does.** The
claim held in practice: PostgreSQL (ADR-0025) needed no rendering divergence at
all beyond its quoting and its plain `limit`/`offset` clause — it delegates to
the reference rendering with every knob at its default.

## Deliberately absent

**GROUP BY does not exist in the AST** and is not planned: aggregations are
written as real SQL in `[Statement]` entities (ADR-0011/0012). Criteria stay a
row-filter/sort/page language, never a full SQL replacement. Joins arrive with
Level 2 eager loading and extend this AST rather than replace it.

## Dynamic composition

The AST is data, so optional filters compose by building the predicate list —
there is no string assembly and no conditional SQL fragments:

```csharp
var where = new List<Criteria>();
if (name is not null) where.Add(Criteria.Like("Name", name + "%"));
if (statuses.Count > 0) where.Add(Criteria.In("Status", statuses));

var rows = await db.Query<Order>().Where(where.ToArray())
    .OrderBy("CreatedAtUtc", SortOrder.Desc).Limit(20).ToListAsync(ct);
```

An empty list is simply a select-all; an empty `statuses` that *is* added renders
`1 = 0` and returns nothing, visibly.

## Conformance cases

`conformance/ast/*.json` pins the rendering, one case per file — pure, no
database:

```json
{ "name": "…", "comment": "…", "entity": "User",
  "select": { "where": [ … ], "orderBy": [ … ], "limit": 20, "offset": 5 },
  "expect": {
    "sqlite":    { "sql": "…", "parameters": [1, "Ada", "Grace"] },
    "sqlserver": { "sql": "…", "parameters": [1, "Ada", "Grace"] } } }
```

The runner builds the AST from `select` (the encodings above), renders it through
**every dialect**, and compares the **exact SQL text** and the **ordered
parameter values** — or, for `"expect": { "error": "QRY-007" }`, the error code.
`expect` carries one entry per dialect and every entry is mandatory (ADR-0024): a
case missing a dialect's expectation fails the suite. Error expectations are
dialect-neutral (refusal happens in the shared rendering, before any dialect
divergence). Expected parameters are the AST values in bind order — the same
order on every dialect, even where a rendered clause places them differently
(SQL Server's `offset @c5 … fetch next @c4`) — their database encoding is
`spec/mapping-rules.md` territory.
