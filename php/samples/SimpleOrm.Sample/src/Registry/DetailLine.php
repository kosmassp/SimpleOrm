<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Registry;

use SimpleOrm\Types\Decimal;

/** One nested line of {@see TransactionWithDetails}: hydrated from the JSON object's snake_case keys by `JsonTypeHandler`. */
final readonly class DetailLine
{
    public function __construct(
        public string $description,
        public int $quantity,
        public Decimal $unitPrice,
    ) {
    }
}
