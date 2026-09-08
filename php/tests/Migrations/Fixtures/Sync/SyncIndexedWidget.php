<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\Sync;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** A `SchemaSyncTest` fixture for structural (not by-name) index matching (ADR-0017 add.2). */
#[Table('sync_indexed_widgets')]
#[Index(['name'])]
final class SyncIndexedWidget
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;

    #[Column]
    public ?string $note = null;
}
