<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * The result of resolving one property's neutral type (§7.9, CODING-STANDARD
 * §10): the loaders build this once per property and carry it into
 * {@see MappedPropertySpec} unchanged — every other subsystem reads `$type` and
 * never re-derives it from `$phpType`.
 */
final readonly class ResolvedPropertyType
{
    public function __construct(
        public ColumnType $type,
        public ?string $phpType,
        public bool $nullable,
    ) {
    }
}
