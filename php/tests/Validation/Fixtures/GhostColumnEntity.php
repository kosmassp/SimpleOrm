<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation\Fixtures;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** Maps the real `users` table plus a column that does not exist there (`VAL-013`). */
#[Table('users')]
final class GhostColumnEntity
{
    #[Key]
    #[Column]
    public int $id;

    #[Column]
    public ?string $ghostColumn = null;
}
