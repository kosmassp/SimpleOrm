<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\BaseApp\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;

/**
 * A concrete class without a relation source that other scanned classes extend: a base class that should have
 * been declared abstract (ADR-0040). A lone load maps it to the convention table `animal`; only the assembly
 * scan can tell, and `export-metadata` / `validate` report `MAP-028`.
 */
class Animal
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;
}
