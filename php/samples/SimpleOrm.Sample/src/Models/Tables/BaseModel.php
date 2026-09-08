<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Models\Tables;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;

/**
 * Audit columns shared by every sample table. Lives in the sample, not the
 * library: SimpleOrm never requires a base class, but supporting one means the
 * metadata loader must map inherited properties. The explicit column names keep
 * the UTC-signaling property names while matching the actual columns
 * (convention alone would produce `created_at_utc`).
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
