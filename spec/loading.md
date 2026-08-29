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
query itself arrives with graph reshaping (Level 2 milestone 4) and follows the
same contract: requested, never inferred.

## The calls

```csharp
await db.LoadAsync(transaction, nameof(Transaction.User), ct);        // one entity
await db.LoadEachAsync(users, nameof(User.Transactions), ct);         // the batch form
```

- The navigation is named by **property name**, exactly (case-sensitive, like
  the declaration). A name that is not a declared navigation is `REL-001`; the
  error lists the declared ones.
- The batch form issues **one query per navigation per call** — never one per
  entity — chunked only past the parameter budget (reference: 500 owners per
  query). Many-to-many is exactly **two** visible queries: link rows, then
  targets.
- All loading SQL goes through the criteria pipeline (query-ast.md): explicit
  column lists, every value parameterized, rendered by the dialect, results
  **ordered by the target key** — compared as **values**, never as a string
  rendering (`10` comes after `2`; ADR-0021 add.1).
- Key and FK tuples match by **structural value equality** — the §7.4 identity
  rule — never by stringified tokens (lossy stringification silently loads the
  wrong entity for date and blob keys).
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
equalities, in key order (metadata-model.md).

## Shape errors (`REL-003`)

Declaration-time validation (metadata-model.md) covers what it can see; shapes
it could not — an FK property that exists on the target type but is not a
mapped column, a link FK that is not a mapped column of the link, an arity
mismatch against a key the target never declared — refuse at load time with
`REL-003` naming the navigation. Nothing is ever loaded best-effort.

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
