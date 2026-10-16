<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * Declares the column this property's column was stored under before (Level 3 M5, spec/migrations.md
 * "Draft migrations"): `#[Column('role_name')] #[RenamedFrom('name')]`. The diff generator reads it as a
 * declared rename, exactly like `--rename`, but kept with the model; the mapper and the session ignore it.
 * Valid only beside `#[Column]` on an entity's own column and with a non-empty name (`MAP-027`).
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class RenamedFrom
{
    public function __construct(
        public string $column,
    ) {
    }
}
