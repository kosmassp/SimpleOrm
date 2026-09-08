<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Registry;

/** Result of `Queries::roleUsage()`: constructor parameters match the result columns by name (§7.8). */
final readonly class RoleUsage
{
    public function __construct(
        public string $roleName,
        public int $userCount,
    ) {
    }
}
