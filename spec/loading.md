# Relationship loading — explicit, batched, never implicit

Nothing loads implicitly (§2, ADR-0019 add.1/0021): **reading an unloaded
navigation never fires SQL** — there are no proxies and no access-triggered
queries. And unloaded is not empty (ADR-0021 add.2): an entity **read from the
database** carries foreign keys proving related rows may exist, so its
collection navigations throw `REL-004` on any access until loaded — loading
(explicit, batch, or eager) replaces the guard, and a loaded-but-empty
collection reads as empty. Entities constructed by user code keep their own
initializers. Singular navigations stay null until loaded where the language
cannot intercept a property read without proxies; after loading, null means a
null foreign key or a **dead link** (the FK points at no row — "there is no
real model to go there" — which loads as null, never as an error). Loading is always an explicit act naming the entity
(or entities), the navigation, and a cancellation token; eager loading with the
query itself (`Include` + a fetch mode, ADR-0022, below) follows the same
contract: requested, never inferred.

**The guard across languages** (ADR-0032). An unloaded collection of a
database-read entity **must not read as empty**. Where the language can
intercept the read (C# property getters; PHP through `__get` on an unset
declared property, via an opt-in trait), it throws `REL-004`. Where it can
only refuse with its own error (PHP without the trait: an uninitialized
typed property), it does that — a throw beats a silent `[]`. Only where the
language can do neither (Go: a slice read cannot be intercepted) does the
port fall back to a documented unloaded-versus-empty distinction (nil versus
empty slice), recorded as a divergence in its coding standard. Loaded-but-
empty always reads as empty, in every language.

## The calls

```csharp
await db.LoadAsync(transaction, nameof(Transaction.User), ct);        // one entity
await db.LoadEachAsync(users, nameof(User.Transactions), ct);         // the batch form
```

- The navigation is named by **property name**, exactly (case-sensitive, like
  the declaration). A name that is not a declared navigation is `REL-001`; the
  error lists the declared ones.
- The batch form issues **one query per navigation per call** — never one per
  entity — chunked only past the parameter budget (reference: 500 per query).
  Chunking counts **distinct complete correlate tuples**, not owners: null
  parts are excluded and duplicates collapse before chunking, so a chunk is
  never larger than 500 owners and usually smaller (ADR-0033). Many-to-many is
  exactly **two** visible queries: link rows, then targets; the link→target hop
  is an ordinary key list and chunks like any other, in every mode (ADR-0031).
- All loading SQL goes through the criteria pipeline (query-ast.md): explicit
  column lists, every value parameterized, rendered by the dialect, results
  **ordered by the target key** — compared as **values**, never as a string
  rendering (`10` comes after `2`; ADR-0021 add.1). Value-wise comparison
  (ordering and identity) means: numbers numerically — decimals included, never
  by their text — strings ordinally, temporals by instant, GUIDs by their bytes,
  booleans false before true (ADR-0031). The SQL `ORDER BY` on the target key is
  only a determinism aid: a key stored as text (decimals, GUIDs on SQLite) sorts
  textually there (`'10'` before `'2'`), so **every loaded collection** —
  one-to-many, many-to-many, join mode — is sorted value-wise **after fetching**
  with the one key comparison, in every mode and for every kind (ADR-0033; the
  reference did this only for many-to-many and join mode until then, and a
  decimal-keyed one-to-many loaded in text order).
- Key and FK tuples match by **structural value equality** — the §7.4 identity
  rule — never by stringified tokens (lossy stringification silently loads the
  wrong entity for date and blob keys). Because correlation is by value,
  **duplicate owners** in a batch call are each filled, and entities sharing a
  key share the loaded instances (ADR-0031).
- An owner whose key contains a null part is excluded from querying; its
  navigations stay empty/null — symmetric with the many-to-one null-FK rule.
- Loading overwrites the navigation with fresh state (a reload is a reload);
  entities the call did not name are untouched.

## Eager loading (`Include` + `Fetch`, ADR-0022 + add.1)

Eager loading is the same contract requested **with the query**: the criteria
chain's `Include(navigations…)` loads the named navigations automatically. The
**fetch mode** chooses how — the modes must load **identical graphs** and
differ only in round trips and data shape ("depends on the need"):

```csharp
var users = await db.Query<User>()
    .Where(Criteria.Ge("CreatedAtUtc", since))
    .Include(nameof(User.Transactions), nameof(User.Profile))
    .Fetch(FetchMode.SubSelect)          // or MultiQuery (default), or Join
    .OrderBy("Id").Limit(20)
    .ToListAsync(ct);
```

| mode | queries | traits |
|---|---|---|
| `MultiQuery` (default) | root + one batched key-list query per navigation | no duplicated data; paging always correct; chunks past the parameter budget |
| `SubSelect` | root + one query per navigation filtering `IN (select … from the root query)` | no owner-side chunking (the many-to-many link→target hop still key-lists); pages correctly — a paged root gains **key-tiebroken ordering** applied to both the root and the subquery, so both evaluations pick the same rows. The subquery re-evaluates the root: rows changing between the two queries can drift, the same window every multi-statement mode has. Composite keys render row-value `IN`; a dialect without row values overrides |
| `Join` | **one** SELECT with LEFT JOINs | fewest round trips; a **collection** include refuses limit/offset (`REL-005` — the join multiplies root rows, and in-memory paging is never acceptable; to-one-only includes page fine) and at most one collection navigation joins (`REL-006` — never a silent Cartesian product); keyless roots/targets refuse (`REL-003` — identity drives the reshaping; load them via MultiQuery). With a **single** included navigation, rows count raw — duplicate-key source rows and `REL-002` behave exactly as in the other modes; with several, identity-dedup cancels the cross-navigation fan-out, and a same-key duplicate source row is then indistinguishable from it — targets whose declared key is not actually unique should load via MultiQuery |

In every mode: roots deduplicate and children share instances by §7.4 identity;
collections order by target key value-wise; one-to-one duplicates are
`REL-002`; an unknown navigation is `REL-001` even when the query matches no
rows; non-included collection navigations keep the `REL-004` guard. The
single-row terminals eager-load their row identically. Includes are
single-level; deeper graphs load explicitly from the loaded entities. The
`json_group_array` nesting pattern (mapping-rules.md) remains the
single-round-trip alternative for arbitrary shapes.

**Refusal precedence, before any SQL** (ADR-0031, ADR-0033): `REL-001`
(unknown navigation), then the join mode's request-shape refusals — `REL-006`
(several collections, the broader problem) before `REL-005` (paging with a
collection) — then `REL-003` (keyless root or target, unmapped FK/link
properties, arity against a runtime key); in join mode `REL-003` for the root
comes first, then `REL-003` per navigation in include order. Shape problems
refuse as `REL-003`; `QRY-006` is reserved for criteria the user wrote.

**SubSelect, precisely** (ADR-0031). *Key-tiebroken ordering*: when the mode is
SubSelect, at least one navigation is included, and the root has a limit or an
offset, the root's orderings gain every key property (ascending, in key order)
not already ordered on; that ordering drives **both** the root query and every
subquery, so the two evaluations pick the same rows. Unpaged roots are
untouched. *Projection*: the subquery projects the owner's correlating
properties — its key for one-to-one/one-to-many/many-to-many, its FK properties
for many-to-one — in key order; composite owners render a row-value membership
(query-ast.md).

**Join, precisely.** *Aliases* (ADR-0031): `j<n>` counts every include in
include order (a many-to-many's projected target join takes the next `j<n>`);
`l<n>` counts the many-to-many includes, separately. *Order*: root rows keep
the order of their **first appearance** in the joined result, which is the
query's ORDER BY (ADR-0031); each root's collections take the same post-fetch
value-wise sort as every other mode — the query's ORDER BY governs only the
roots' first-appearance order, since the AST orders root properties only and
there is no per-join ordering (ADR-0033). *Absent targets*: an owner whose FK
part is null, or whose FK points at no row, reads an all-NULL target segment
and loads as null/absent — a null FK and a dead link are indistinguishable
here, as everywhere (ADR-0031). *`REL-002`* fires when one root sees more than
one distinct target key under a one-to-one alias, in every include combination;
children deduplicate by target key, and a keyed target cannot repeat a key, so
deduplication hides nothing a keyed table can produce (ADR-0033).

Conformance: load cases marked `"viaQuery": true` replay through `Include`
under **all three modes** against the same `loaded` expectations.

## Per kind

| kind | fills | notes |
|---|---|---|
| many-to-one | the single target for the owner's FK tuple, or null when no row matches | owners sharing a target within one call share the **same instance**; an owner with any null FK part keeps a null navigation and binds nothing |
| one-to-one | the single target whose FK equals the owner's key, or null | **more than one matching row is `REL-002`** — the unique index on the target FK is what makes a 1:1, and drift is refused, never resolved by picking one |
| one-to-many | a fresh list of the targets whose FK equals the owner's key — empty, never null | ordered by target key |
| many-to-many | the targets referenced by the declared link's rows for this owner | two queries; de-duplicated; ordered by target key (value-wise). A link row whose target row does not exist contributes nothing — the loaded collection reflects existing rows, exactly as a join would; referential integrity is the database's story, not loading's (ADR-0021 add.1) |

Composite keys use the same paths: FK tuples compare as OR-ed groups of ANDed
equalities, in key order (metadata-model.md). A composite many-to-many link's
FK declarations to a side are in declaration order, and that order pairs with
the side's key parts in key order — a link must declare them in key order
(ADR-0031).

## Shape errors (`REL-003`)

Declaration-time validation (metadata-model.md) covers what it can see; shapes
it could not — an FK property that exists on the target type but is not a
mapped column, a link FK that is not a mapped column of the link, an arity
mismatch against a key the target never declared — refuse at load time with
`REL-003` naming the navigation. The check applies to **every kind** at load
time, many-to-one included, even where declaration-time validation (`MAP-016`)
already checked the arity — the loader checks the shape it is about to query,
uniformly (ADR-0033). Nothing is ever loaded best-effort.

## Conformance cases

`conformance/load-cases/*.json`: owner keys in, loaded values out, against the
seeded fixture database:

```json
{ "name": "…", "load": { "entity": "User", "navigation": "Transactions", "keys": [1, 2] },
  "expect": { "loaded": {
    "1": [ { "id": 1, "user_id": 1 } ],
    "2": [ ] } } }
```

`loaded` maps each owner key to the expectation: an object (or `null`) for
singular navigations, an array **ordered by target key** for collections.
Values are keyed by column name in the conformance value encoding
(mapping-rules.md); listed columns are checked, others ignored; array lengths
must match exactly. A key is an integer, or an **array of parts in key order**
for composite-key owners — in `loaded`, composite keys join their parts with
`|`. `"expect": { "error": "REL-001" }` pins refusals; `REL-002`/`REL-003` need
drifted data or shape-broken metadata the case format cannot seed, so each
implementation pins them in its own tests.

`load.navigation` is spelled as the reference declares it (`Transactions`); a
port whose declaration casing differs (PHP `$transactions`) maps the name in
its runner, as the renderer's case-insensitive property resolution does for
criteria. The library call itself stays exact to the declaration (`REL-001`
otherwise) (ADR-0033).

An **explicit** replay reads each of `load.keys` by key and issues one batch
`LoadEach` over the list (ADR-0033). A `viaQuery` replay's root query selects
exactly the listed owners: for a single-column key, `Where(In(<key property>,
keys))`; for a composite key, an `Or` of one `And` of equalities per owner,
parts in key order. It includes the case's navigation, runs under each fetch
mode in turn, and checks the same `loaded` expectations — including an expected
error, which every mode must raise even though the query would match no rows.

## Where the Clarifications went

The Clarifications blocks the Level 2 ports produced were folded into the
sections they amend (ADR-0046); every rule is stated there once.

| Clarification (block) | Now in |
|---|---|
| Key-tiebroken ordering, precisely (ADR-0031) | "Eager loading" — **SubSelect, precisely** |
| SubSelect projection (ADR-0031) | "Eager loading" — **SubSelect, precisely** |
| The many-to-many link→target hop (ADR-0031) | "The calls" — the batch form |
| Duplicate owners (ADR-0031) | "The calls" — structural value equality |
| Value-wise comparison (ADR-0031) | "The calls" — ordering |
| Join mode aliases (ADR-0031) | "Eager loading" — **Join, precisely** |
| Refusal precedence, before any SQL (ADR-0031) | "Eager loading" — **Refusal precedence, before any SQL** |
| Composite many-to-many links (ADR-0031) | "Per kind" — composite keys |
| Value-wise ordering is a post-fetch sort (ADR-0033) | "The calls" — ordering |
| Chunking counts distinct complete correlate tuples (ADR-0033) | "The calls" — the batch form |
| `REL-003` applies to every kind at load time (ADR-0033) | "Shape errors (`REL-003`)" |
| Join mode orders refusals `REL-006` before `REL-005` (ADR-0033) | "Eager loading" — **Refusal precedence, before any SQL** |
| Join-mode collection order (ADR-0033) | "Eager loading" — **Join, precisely** |
| `REL-002` in join mode (ADR-0033) | "Eager loading" — **Join, precisely** |
| Conformance case names (ADR-0033) | "Conformance cases" |
| Explicit replay of a load case (ADR-0033) | "Conformance cases" |
