<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/** Declares a table-backed entity (§7.2): the one writable relation source (ADR-0008). */
#[Attribute(Attribute::TARGET_CLASS)]
final readonly class Table
{
    public function __construct(
        public string $name,
        public ?string $schema = null,
    ) {
    }
}
