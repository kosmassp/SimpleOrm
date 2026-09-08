<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\App\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** Table `app_gadgets` — the `Application` end-to-end fixture's second entity, added by `V0002_Create`. */
#[Table('app_gadgets')]
#[Index(['widgetId'])]
final class Gadget
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(Widget::class)]
    public int $widgetId;

    #[Column]
    public string $label;
}
