<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session\Fixtures;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\ColumnType;

/**
 * A tracked-session fixture with a handler-typed member (spec/session.md Clarifications, ADR-0035): `settings`
 * is a JSON TEXT column read and written through a registered {@see \SimpleOrm\Mapping\JsonTypeHandler}.
 */
#[Table('documents')]
final class Document
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(type: ColumnType::String)]
    public Settings $settings;
}
