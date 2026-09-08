<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** A column's shape as compared by `MigrationGenerator::diff()` (ADR-0017) — name, storage type, nullability. */
final readonly class ColumnSpec
{
    public function __construct(
        public string $name,
        public string $storageType,
        public bool $nullable,
    ) {
    }
}
