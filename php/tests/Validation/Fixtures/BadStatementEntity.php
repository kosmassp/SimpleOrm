<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation\Fixtures;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Statement;

/**
 * A well-formed `#[Statement]` entity on its face — `hasMappingAttributes()`
 * sees this attribute and lets SchemaGuard consider it. The test that uses
 * this fixture overrides its loaded `EntityMap` via
 * `MappingOptions::$explicitMaps` with SQL/declared-parameter pairs that
 * disagree, so SchemaGuard's own `PRM-012` re-check (not the attribute
 * loader's `PRM-010`/`PRM-011`, which this bypasses) is what has to catch it.
 */
#[Statement('select 1 as one')]
final class BadStatementEntity
{
    #[Column]
    public int $one;
}
