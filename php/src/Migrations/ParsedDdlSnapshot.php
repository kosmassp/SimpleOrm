<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** The result of `SchemaSnapshot::parseDdl()` (ADR-0017 add.1): a view/materialized-view/procedure's normalized defining DDL, as of a version. */
final readonly class ParsedDdlSnapshot
{
    public function __construct(
        public string $object,
        public string $kind,
        public string $ddl,
        public int $asOfVersion,
    ) {
    }
}
