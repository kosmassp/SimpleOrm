<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\SqlServer;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\ColumnType;

/** The client-GUID key strategy (`uniqueidentifier not null primary key`); mirrors the session tests' GuidDocFixture. */
#[Table('ss_guid_docs')]
final class GuidDoc
{
    #[Key]
    #[Column(type: ColumnType::Guid)]
    public string $id;

    #[Column]
    public string $title;
}
