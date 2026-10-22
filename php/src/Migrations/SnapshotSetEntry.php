<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialects;

/**
 * One object's snapshot at one version (ADR-0017): exactly one of `$table`/`$ddl`/`$trigger` is set, mirroring
 * `SchemaSnapshot`'s three shapes — columns (tables), normalized DDL (views, materialized views, procedures), and the
 * neutral declaration (triggers, ADR-0042). `$dialect` is the snapshot's stamp (format v3, ADR-0043; `sqlite` when
 * unstamped), which every consumer compares against the dialect in use (`MIG-014`).
 */
final readonly class SnapshotSetEntry
{
    public function __construct(
        public ?TableSchema $table,
        public ?string $ddl,
        public ?TriggerSchema $trigger = null,
        public string $dialect = Dialects::SQLITE,
    ) {
    }
}
