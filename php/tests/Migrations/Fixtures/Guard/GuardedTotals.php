<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\Guard;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\View;

/** A `ViewGuardTest` fixture (ADR-0017 add.1): only the relation name matters — the step under test renders its own literal DDL. */
#[View('guarded_totals', 'select 2 as answer')]
final class GuardedTotals
{
    #[Column]
    public int $answer;
}
