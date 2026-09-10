<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

/**
 * One ON equality of a {@see SelectJoin} (ADR-0022 add.1, spec/query-ast.md):
 * `parent.<parentProperty> = target.<targetProperty>`, both **property** names
 * resolved through the respective maps by the renderer (mirrors the C#
 * `(ParentProperty, TargetProperty)` tuple — PHP has no tuples, so the pair is
 * a value class).
 */
final readonly class JoinPair
{
    public function __construct(
        public string $parentProperty,
        public string $targetProperty,
    ) {
    }
}
