<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** The result of `SchemaSnapshot::parse()` (ADR-0017): a table's shape plus the version it was recorded as of. */
final readonly class ParsedTableSnapshot
{
    public function __construct(
        public TableSchema $schema,
        public int $asOfVersion,
    ) {
    }
}
