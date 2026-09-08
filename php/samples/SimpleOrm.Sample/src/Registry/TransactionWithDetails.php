<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Registry;

use SimpleOrm\Types\Decimal;

/**
 * Result of `Queries::transactionsWithDetails()`: a raw-SQL DTO nesting
 * {@see DetailLine} rows via `json_group_array` (§7.10). `$details` is
 * `array`-typed (PHP has no generics); the fully-qualified
 * `@param list<\...DetailLine>` docblock is how the mapper derives the JSON
 * handler's lookup key, so it must match `DetailLine::class` verbatim
 * (CODING-STANDARD §10).
 */
final readonly class TransactionWithDetails
{
    /**
     * @param list<\SimpleOrm\Sample\Registry\DetailLine> $details
     */
    public function __construct(
        public int $id,
        public Decimal $amount,
        public array $details,
    ) {
    }
}
