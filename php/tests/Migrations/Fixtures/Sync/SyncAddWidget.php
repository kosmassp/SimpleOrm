<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\Sync;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/**
 * A `SchemaSyncTest` fixture: the live table (created by the test with a raw
 * `junk` column and no `note`) exercises both additive (`note` missing,
 * nullable) and deletion (`junk` unmapped) planning in one table.
 */
#[Table('sync_add_widgets')]
final class SyncAddWidget
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
