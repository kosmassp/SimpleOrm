<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\App\Models;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;

/**
 * An abstract base carrying a `#[Column]`, the sample-style `BaseModel` shape:
 * it contributes columns to subclasses but is no entity itself. The CLI's
 * type discovery must skip it (`Application::mappedTypes()`, mirroring the
 * reference's `IsClass && !IsAbstract`) rather than refuse with `MAP-019`.
 */
abstract class AuditedModel
{
    #[Column('created_at')]
    public DateTimeImmutable $createdAtUtc;
}
