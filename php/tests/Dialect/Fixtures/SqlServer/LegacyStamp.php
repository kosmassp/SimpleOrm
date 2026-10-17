<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\SqlServer;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** Mapped over a legacy `datetime2` column: type-compatible, but every value refuses `VAL-020` (§7.9, spec/dialects.md "Temporals"). */
#[Table('ss_legacy_stamps')]
final class LegacyStamp
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public DateTimeImmutable $stampedAt;
}
