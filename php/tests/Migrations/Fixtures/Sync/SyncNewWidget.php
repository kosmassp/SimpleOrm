<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\Sync;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** A `SchemaSyncTest` fixture: no table exists yet, so the whole thing is additive. */
#[Table('sync_new_widgets')]
final class SyncNewWidget
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;
}
