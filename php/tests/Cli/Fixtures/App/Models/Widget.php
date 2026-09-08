<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\App\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** Table `app_widgets` — the `Application` end-to-end fixture's first entity. Never changes after `V0001_Create`. */
#[Table('app_widgets')]
final class Widget
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;
}
