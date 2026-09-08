<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/**
 * Table `widgets` — the shadow-replayer fixture. `note` is added by
 * `V0003_AddNote`, so the current shape (with `note`) is what every future
 * `createTable()` call would render; `V0001_Create` therefore freezes the V1
 * shape as literal SQL instead (§7.22 — metadata-rendered creates are only
 * safe while the object never changes again).
 */
#[Table('widgets')]
final class Widget
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
