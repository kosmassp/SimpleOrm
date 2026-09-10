<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use LogicException;
use SimpleOrm\Query\SelectAst;

/**
 * Join-mode eager loading (ADR-0022 add.1) — the PHP counterpart of the C# `Db`
 * partial in `DbEagerJoin.cs` (a collaborator here, CODING-STANDARD §10): one
 * SELECT with a LEFT JOIN per included navigation (a many-to-many joins its
 * link unprojected, then the target), the row partitioned into per-alias
 * segments that reuse the one mapping pipeline (§7.11), roots deduplicated by
 * key in first-appearance order, children attached by structural key
 * equality. Refuses paging with a collection include (`REL-005`), more than
 * one collection include (`REL-006`), and keyless roots/targets (`REL-003`) —
 * never in-memory paging or a silent Cartesian product.
 */
final class DbEagerJoin
{
    public function __construct(private readonly Db $db)
    {
    }

    /**
     * @param class-string $entityType
     * @param list<string> $includes navigation names, already validated (`REL-001`)
     * @return list<object>
     */
    public function load(SelectAst $ast, string $entityType, array $includes): array
    {
        throw new LogicException('DbEagerJoin::load is the Level 2 join engine — not implemented yet (ADR-0032 step 2c)');
    }
}
