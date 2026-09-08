<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;

/**
 * Audit columns shared by every sample table (mirrors dotnet/samples BaseModel):
 * inherited properties map, ordered after the most-derived class's own. The
 * explicit column names keep the UTC-signaling property names while matching
 * the actual columns (convention alone would give `created_at_utc`).
 */
abstract class BaseModel
{
    /** ISO-8601 UTC TEXT in the database (§7.9): a DateTimeImmutable in UTC. */
    #[Column('created_at')]
    public DateTimeImmutable $createdAtUtc;

    /** Null until the row is first updated. */
    #[Column('updated_at')]
    public ?DateTimeImmutable $updatedAtUtc = null;
}
