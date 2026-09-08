<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation\Fixtures;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** Maps a table that does not exist in the fixture database (`VAL-012`). */
#[Table('nope_table')]
final class MissingRelationEntity
{
    #[Key]
    #[Column]
    public int $id;
}
