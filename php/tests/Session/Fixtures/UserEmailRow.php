<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session\Fixtures;

/** A plain DTO result (no mapping attributes): constructor-based binding by name (§7.8). */
final readonly class UserEmailRow
{
    public function __construct(
        public int $id,
        public string $email,
    ) {
    }
}
