<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\TriggerApp\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\Trigger;

#[Table('first_tables')]
#[Trigger(SharedTrigger::class)]
final class FirstTable
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;
}
