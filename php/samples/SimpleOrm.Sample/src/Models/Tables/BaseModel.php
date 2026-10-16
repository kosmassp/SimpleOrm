<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Models\Tables;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\CreatedAt;
use SimpleOrm\Metadata\Attributes\UpdatedAt;

/**
 * Audit columns shared by every sample table. Lives in the sample, not the
 * library: SimpleOrm never requires a base class, but supporting one means the
 * metadata loader must map inherited properties. The explicit column names keep
 * the UTC-signaling property names while matching the actual columns
 * (convention alone would produce `created_at_utc`).
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
