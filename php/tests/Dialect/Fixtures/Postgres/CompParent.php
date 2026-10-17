<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\OneToMany;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Session\Navigations;

/** A two-column natural key: its subselect include renders native row-value `IN` on PostgreSQL. */
#[Table('comp_parents')]
final class CompParent
{
    use Navigations;

    #[Key]
    #[Column]
    public int $aId;

    #[Key]
    #[Column]
    public int $bId;

    #[Column]
    public string $label;

    #[OneToMany(CompChild::class, 'parentA', 'parentB')]
    public private(set) array $children = [];
}
