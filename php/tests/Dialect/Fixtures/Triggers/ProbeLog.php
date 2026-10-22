<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Triggers;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** The log the probe triggers write (the trigger live legs, ADR-0042): one row per firing, carrying the probe's name. */
#[Table('probe_logs')]
final class ProbeLog
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;
}
