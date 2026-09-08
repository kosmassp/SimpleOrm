<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation\Fixtures;

/** Result fixture for {@see BadRegistry::wrongShape()}: the SQL's extra `mystery` column matches nothing (`MAP-001`). */
final readonly class WrongShapeRow
{
    public function __construct(
        public int $id,
        public string $name,
    ) {
    }
}
