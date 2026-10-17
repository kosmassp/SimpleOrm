<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** Mapped over a legacy `timestamp without time zone` column: type-compatible, but every value refuses `VAL-020` (§7.9). */
#[Table('legacy_stamps')]
final class LegacyStamp
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public DateTimeImmutable $stampedAt;
}
