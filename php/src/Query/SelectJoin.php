<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

use SimpleOrm\Metadata\EntityMap;

/**
 * One joined relation of a select (ADR-0022 add.1 — join-mode eager loading):
 * LEFT JOIN `target` aliased `alias`, ON equality pairs between the parent's
 * properties and the target's. Only projected joins contribute columns (a
 * many-to-many's link joins without projecting). Mirrors the C# `SelectJoin`.
 */
final readonly class SelectJoin
{
    /**
     * @param string|null $parentAlias the alias this join hangs off; null joins to the root
     * @param list<JoinPair> $on equality pairs: parent property = target property
     * @param bool $project whether the join's columns are selected (aliased `alias_column`)
     */
    public function __construct(
        public EntityMap $target,
        public string $alias,
        public ?string $parentAlias,
        public array $on,
        public bool $project,
    ) {
    }
}
