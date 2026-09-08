<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\Sync;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** A `SchemaSyncTest` fixture: the model wants `note TEXT`, the live table has `note INTEGER` — never auto-applied. */
#[Table('sync_bad_widgets')]
final class SyncBadWidget
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public ?string $note = null;
}
