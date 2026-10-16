<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\CreatedAt;
use SimpleOrm\Metadata\Attributes\UpdatedAt;

/**
 * Audit columns shared by every sample table (mirrors dotnet/samples BaseModel):
 * inherited properties map, ordered after the most-derived class's own. The
 * explicit column names keep the UTC-signaling property names while matching
 * the actual columns (convention alone would give `created_at_utc`).
 */
abstract class BaseModel
{
    /** ISO-8601 UTC TEXT in the database (§7.9): a DateTimeImmutable in UTC; the session fills it on insert when unset (spec/hooks.md). */
    #[Column('created_at')]
    #[CreatedAt]
    public DateTimeImmutable $createdAtUtc;

    /** Null until the row is first updated; the session stamps it (spec/hooks.md). */
    #[Column('updated_at')]
    #[UpdatedAt]
    public ?DateTimeImmutable $updatedAtUtc = null;
}
