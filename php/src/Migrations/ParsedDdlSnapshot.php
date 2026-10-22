<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialects;

/** The result of `SchemaSnapshot::parseDdl()` (ADR-0017 add.1): a view/materialized-view/procedure's normalized defining DDL, as of a version, with its dialect stamp (ADR-0043; `sqlite` when unstamped). */
final readonly class ParsedDdlSnapshot
{
    public function __construct(
        public string $object,
        public string $kind,
        public string $ddl,
        public int $asOfVersion,
        public string $dialect = Dialects::SQLITE,
    ) {
    }
}
