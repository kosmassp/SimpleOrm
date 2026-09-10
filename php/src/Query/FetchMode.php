<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

/**
 * How {@see CriteriaQuery::include()} fetches (ADR-0022 add.1, owner: "it will
 * depend on the need"). All three produce identical loaded graphs; they differ
 * in round trips and data shape:
 * - `MultiQuery` (default): root query + one batched query per navigation —
 *   no duplicated data, paging always correct.
 * - `SubSelect`: like MultiQuery, but each navigation's owner filter is
 *   `in (select … from the root query)` instead of a client-side key list — no
 *   owner-side chunking and correct paging (a paged root gains key-tiebroken
 *   ordering so both evaluations pick the same rows; the many-to-many
 *   link→target hop still key-lists client-side).
 * - `Join`: one SELECT with LEFT JOINs — fewest round trips; a collection
 *   include refuses paging (`REL-005`, never in-memory — to-one includes page
 *   fine) and at most one collection navigation joins (`REL-006`, never a
 *   Cartesian product); keyless roots/targets refuse (`REL-003`).
 */
enum FetchMode
{
    case MultiQuery;
    case SubSelect;
    case Join;
}
