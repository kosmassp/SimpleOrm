<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * A table's shape, provider-agnostic but in **storage types** (what CREATE
 * TABLE emits, ADR-0017). Both snapshot producers build this: the metadata
 * exporter (`SchemaSnapshot::fromMap()`, the `snapshot` command) and the
 * `shadow` replayer's introspection — which is what makes the two comparable.
 * `SchemaSnapshot::export()` name-sorts columns and indexes for determinism;
 * this value carries them as declared/introspected.
 */
final readonly class TableSchema
{
    /**
     * @param list<TableSchemaColumn> $columns
     * @param list<TableSchemaIndex> $indexes
     */
    public function __construct(
        public string $name,
        public array $columns,
        public array $indexes,
    ) {
    }
}
