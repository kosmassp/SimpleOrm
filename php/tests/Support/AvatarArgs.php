<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\ColumnType;

/**
 * A registry argument declaring its `bytes` token the way an entity property does
 * (`#[Column(type:)]`, CODING-STANDARD §10) — without it the string would bind as text
 * (ADR-0040 add.1). Used by {@see BytesRoundTrip}.
 */
final readonly class AvatarArgs
{
    public function __construct(
        #[Column(type: ColumnType::Bytes)]
        public string $avatar,
    ) {
    }
}
