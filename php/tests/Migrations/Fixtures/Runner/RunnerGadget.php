<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\Runner;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/** A `MigrationRunnerTest` fixture: only the table name matters — the step under test never creates it from metadata. */
#[Table('runner_gadgets')]
final class RunnerGadget
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;
}
