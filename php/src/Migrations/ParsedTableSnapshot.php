<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialects;

/** The result of `SchemaSnapshot::parse()` (ADR-0017): a table's shape plus the version it was recorded as of and the dialect stamp (ADR-0043; `sqlite` when unstamped). */
final readonly class ParsedTableSnapshot
{
    public function __construct(
        public TableSchema $schema,
        public int $asOfVersion,
        public string $dialect = Dialects::SQLITE,
    ) {
    }
}
