<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\ColumnType;

/**
 * The three temporal tokens a `DateTimeImmutable` can carry besides `datetime`, over their native PostgreSQL
 * columns (`date`, `time without time zone`, `timestamp with time zone`): the write side must store the value's
 * own calendar date and time of day, never the instant shifted to UTC (spec/dialects.md "Temporals").
 */
#[Table('calendar_rows')]
final class CalendarRow
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(type: ColumnType::Date)]
    public DateTimeImmutable $day;

    #[Column(type: ColumnType::Time)]
    public DateTimeImmutable $clock;

    #[Column(type: ColumnType::DateTimeOffset)]
    public DateTimeImmutable $atOffset;
}
