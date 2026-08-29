# Relationship loading — explicit, batched, never implicit

Nothing loads implicitly (§2, ADR-0019 add.1/0021): a navigation holds its
initialized empty/null value until a load call fills it, and **reading an
unloaded navigation never fires SQL** — there are no proxies and no
access-triggered queries. Loading is always an explicit act naming the entity
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

## Eager loading (`Include`, ADR-0022)

Eager loading is the same contract requested **with the query**: the criteria
chain's `Include(navigations…)` runs the root query and then one batch load per
included navigation — multi-query, never a row-multiplying join, so paging
stays correct and every round trip stays visible and countable
(1 + one per navigation; many-to-many: two).

```csharp
var users = await db.Query<User>()
    .Where(Criteria.Ge("CreatedAtUtc", since))
    .Include(nameof(User.Transactions), nameof(User.Profile))
    .OrderBy("Id").Limit(20)
    .ToListAsync(ct);
```

The single-row terminals eager-load their row identically. An unknown
navigation is `REL-001` even when the query matches no rows. Includes are
single-level (a navigation of the root); deeper graphs load explicitly from the
loaded entities. The `json_group_array` nesting pattern (mapping-rules.md)
remains the single-round-trip alternative.

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
