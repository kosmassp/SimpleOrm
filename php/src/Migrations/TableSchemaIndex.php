<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** One index of a `TableSchema`. Identity for diffing is structural (`MigrationGenerator::indexSignature()`), never the name (ADR-0017 add.2). */
final readonly class TableSchemaIndex
{
    /** @param list<TableSchemaIndexPart> $columns */
    public function __construct(
        public string $name,
        public array $columns,
        public bool $unique = false,
    ) {
    }
}
