<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\MaterializedView;

/** A materialized view over the sample `users` table (ADR-0025: alive on PostgreSQL, `DDL-002` on SQLite). */
#[MaterializedView('user_name_counts', <<<'SQL'
    select name as user_name, count(*) as user_count
    from users
    group by name
    SQL)]
final class NameCount
{
    #[Key]
    #[Column]
    public string $userName;

    #[Column]
    public int $userCount;
}
