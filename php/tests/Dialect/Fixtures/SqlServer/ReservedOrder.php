<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\SqlServer;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** Reserved-word identifiers end to end (spec/dialects.md "Testing contract"): table `order`, columns `desc` and `group`. */
#[Table('order')]
final class ReservedOrder
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column('desc')]
    public string $description;

    #[Column('group')]
    public ?string $grouping = null;
}
