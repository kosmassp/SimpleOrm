<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialects;

/** The result of `SchemaSnapshot::parseTrigger()` (ADR-0042): a trigger's neutral declaration plus the version it was recorded as of and its dialect stamp (ADR-0043; `sqlite` when unstamped). */
final readonly class ParsedTriggerSnapshot
{
    public function __construct(
        public TriggerSchema $schema,
        public int $asOfVersion,
        public string $dialect = Dialects::SQLITE,
    ) {
    }
}
