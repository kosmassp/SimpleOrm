<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance\AmendFixture;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/**
 * The `diff --amend` conformance fixture (ADR-0017 add.3,
 * conformance/amend-cases/README.md): table `amend_widgets` with a generated
 * key, `name`, and a nullable `remark` — the model has moved on to `remark`
 * since the draft `V0002` (which added `note`) was generated. `V0001`/`V0002`
 * below, and their table steps, are the "compiled" truth `DiffCommand` reads
 * through `MigrationSet::fromDirectory()` — independent of whatever a given
 * case lays into its own ephemeral migrations directory.
 */
#[Table('amend_widgets')]
final class AmendWidget
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;

    #[Column]
    public ?string $remark = null;
}
