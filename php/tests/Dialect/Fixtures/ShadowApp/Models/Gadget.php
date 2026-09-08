<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** Table `gadgets`: never changes after its V0002 creation, so `createTable()` stays metadata-rendered. */
#[Table('gadgets')]
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
