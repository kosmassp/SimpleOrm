<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\SqlServer;

/** Args of {@see SqlServerRegistry::userById()}. */
final readonly class ByIdArgs
{
    public function __construct(public int $id)
    {
    }
}
