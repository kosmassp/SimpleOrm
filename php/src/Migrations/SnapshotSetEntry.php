<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * One object's snapshot at one version (ADR-0017): exactly one of `$table`/`$ddl`/`$trigger` is set, mirroring
 * `SchemaSnapshot`'s three shapes — columns (tables), normalized DDL (views, materialized views, procedures), and the
 * neutral declaration (triggers, ADR-0042).
 */
final readonly class SnapshotSetEntry
{
    public function __construct(
        public ?TableSchema $table,
        public ?string $ddl,
        public ?TriggerSchema $trigger = null,
    ) {
    }
}
