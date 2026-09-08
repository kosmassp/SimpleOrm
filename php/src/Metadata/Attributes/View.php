<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/** A view-backed, read-only entity carrying its defining SELECT (ADR-0008 add.3). No `#[Index]` (`MAP-014`). */
#[Attribute(Attribute::TARGET_CLASS)]
final readonly class View
{
    public function __construct(
        public string $name,
        public string $sql,
        public ?string $schema = null,
    ) {
    }
}
