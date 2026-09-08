<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session\Fixtures;

use SimpleOrm\Types\Decimal;

/** One nested detail line, hydrated by {@see \SimpleOrm\Mapping\JsonTypeHandler} (§7.10). */
final readonly class DetailLine
{
    public function __construct(
        public string $description,
        public int $quantity,
        public Decimal $unitPrice,
    ) {
    }
}
