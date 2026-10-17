<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\SqlServer;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\ColumnType;

/** A natural string key (takes the indexable `nvarchar(450)`), a nullable byte column, and a table name that needs escaping. */
#[Table("it's")]
final class NaturalKeyed
{
    #[Key]
    #[Column]
    public string $code;

    #[Column(type: ColumnType::Bytes)]
    public ?string $payload = null;
}
