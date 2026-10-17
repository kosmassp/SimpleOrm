<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\SqlServer;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** The many side of {@see CompParent::$children}: one FK property per key part, in key order. */
#[Table('ss_comp_children')]
final class CompChild
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column('parent_a')]
    #[ForeignKey(CompParent::class)]
    public int $parentA;

    #[Column('parent_b')]
    #[ForeignKey(CompParent::class)]
    public int $parentB;

    #[Column]
    public string $note;
}
