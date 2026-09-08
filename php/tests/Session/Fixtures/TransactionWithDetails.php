<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session\Fixtures;

use SimpleOrm\Types\Decimal;

/**
 * A raw-SQL result DTO nesting {@see DetailLine} rows via `json_group_array`
 * (§7.10). `$details` is `array`-typed (PHP has no generics); the
 * fully-qualified `@param list<\...DetailLine>` docblock is how
 * {@see \SimpleOrm\Mapping\ResultMapper} derives the JSON handler's lookup key
 * — it must match `DetailLine::class` verbatim (CODING-STANDARD §10).
 */
final readonly class TransactionWithDetails
{
    /**
     * @param list<\SimpleOrm\Tests\Session\Fixtures\DetailLine> $details
     */
    public function __construct(
        public int $id,
        public Decimal $amount,
        public array $details,
    ) {
    }
}
