<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;

/**
 * The criteria query as data (§10.4, ADR-0012/0020, spec/query-ast.md): source
 * metadata, an implicitly ANDed predicate list, orderings, and paging. Every
 * front-end produces this and never SQL text; the dialect renders it
 * (`Dialect::selectSql`). Property names, not column names — the renderer
 * resolves them through the metadata (`QRY-006`). GROUP BY is deliberately
 * absent — aggregations are `#[Statement]` entities. Joins and projections
 * (ADR-0022 add.1, Level 2) serve eager loading and subqueries; no front-end
 * exposes them directly.
 */
final readonly class SelectAst
{
    /**
     * @param list<Criteria> $where predicates, implicitly ANDed; empty means no WHERE
     * @param list<Ordering> $orderings
     * @param list<PropertyMap>|null $projection the projected properties of the root, or null for every
     *        mapped column (a subquery selects only what it feeds)
     * @param list<SelectJoin> $joins LEFT JOINs for join-mode eager loading; empty for plain queries
     */
    public function __construct(
        public EntityMap $map,
        public array $where,
        public array $orderings,
        public ?int $limit = null,
        public ?int $offset = null,
        public ?array $projection = null,
        public array $joins = [],
    ) {
    }
}
