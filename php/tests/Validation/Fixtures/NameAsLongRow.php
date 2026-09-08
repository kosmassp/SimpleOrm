<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation\Fixtures;

/** Result fixture for {@see BadRegistry::declaredTypeMismatch()}: `users.name` is TEXT, incompatible with `int` (`VAL-011`). */
final readonly class NameAsLongRow
{
    public function __construct(public int $name)
    {
    }
}
