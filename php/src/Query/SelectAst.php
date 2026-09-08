<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

use SimpleOrm\Metadata\EntityMap;

/**
 * The criteria query as data (§10.4, ADR-0012/0020, spec/query-ast.md): source
 * metadata, an implicitly ANDed predicate list, orderings, and paging. Every
 * front-end produces this and never SQL text; the dialect renders it
 * (`Dialect::selectSql`). Property names, not column names — the renderer
 * resolves them through the metadata (`QRY-006`). Joins and projections are
 * Level 2 and absent from the PHP port (CLAUDE.md §12).
 */
final readonly class SelectAst
{
    /**
     * @param list<Criteria> $where predicates, implicitly ANDed; empty means no WHERE
     * @param list<Ordering> $orderings
     */
    public function __construct(
        public EntityMap $map,
        public array $where,
        public array $orderings,
        public ?int $limit = null,
        public ?int $offset = null,
    ) {
    }
}
