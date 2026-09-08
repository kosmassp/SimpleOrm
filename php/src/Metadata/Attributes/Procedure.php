<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;
use SimpleOrm\Metadata\ColumnType;

/** A procedure-backed, read-only entity (ADR-0008 add.): name, body SQL, declared parameters. Dormant on SQLite. */
#[Attribute(Attribute::TARGET_CLASS)]
final readonly class Procedure
{
    /** @param array<string, ColumnType> $parameters */
    public function __construct(
        public string $name,
        public string $sql,
        public array $parameters = [],
        public ?string $schema = null,
    ) {
    }
}
