<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session\Fixtures;

/** A mutable JSON-document value for {@see Document::$settings}, stored through {@see \SimpleOrm\Mapping\JsonTypeHandler}. */
final class Settings
{
    public function __construct(
        public string $theme = 'dark',
        public int $size = 12,
    ) {
    }
}
