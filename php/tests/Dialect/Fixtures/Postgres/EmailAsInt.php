<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

/** A deliberately wrong result shape: `email` is `text` in the schema, an `int` here (`VAL-011`). */
final readonly class EmailAsInt
{
    public function __construct(
        public int $id,
        public int $email,
    ) {
    }
}
