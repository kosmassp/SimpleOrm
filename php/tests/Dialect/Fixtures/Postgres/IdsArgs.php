<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

/** A collection argument that binds as one PostgreSQL array (`where id = any(@ids)`, spec/dialects.md "Array parameters"). */
final readonly class IdsArgs
{
    /** @param list<int> $ids */
    public function __construct(public array $ids)
    {
    }
}
