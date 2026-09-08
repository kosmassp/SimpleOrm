<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation\Fixtures;

/** Args fixture for {@see BadRegistry::wrongParams()}: `id` is never used by that query's SQL (`PRM-002`). */
final readonly class ProbeArgs
{
    public function __construct(public int $id)
    {
    }
}
