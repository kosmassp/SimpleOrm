<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * Loader-internal working shape of one declared `#[Index]` before its property
 * names resolve to column names (§7.2; mirrors the C# reference's `IndexSpec`).
 */
final readonly class IndexSpec
{
    /** @param list<array{0: string, 1: bool}> $columns property name and descending flag, index order */
    public function __construct(
        public ?string $name,
        public bool $unique,
        public array $columns,
    ) {
    }
}
