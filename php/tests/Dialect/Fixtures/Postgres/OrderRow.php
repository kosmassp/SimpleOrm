<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** Reserved-word identifiers end to end (spec/dialects.md "Testing contract"): table `order`, column `desc`. */
#[Table('order')]
final class OrderRow
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column('desc')]
    public string $description;

    #[Column('select')]
    public ?string $selection = null;
}
