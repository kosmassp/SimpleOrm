<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** One object's snapshot at one version (ADR-0017): exactly one of `$table`/`$ddl` is set, mirroring `SchemaSnapshot`'s two shapes. */
final readonly class SnapshotSetEntry
{
    public function __construct(
        public ?TableSchema $table,
        public ?string $ddl,
    ) {
    }
}
