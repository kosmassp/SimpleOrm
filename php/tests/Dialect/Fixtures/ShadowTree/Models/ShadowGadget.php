<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\Trigger;

/**
 * The table of the shadow round trip (ADR-0043): created by a dialect's literal SQL with a unique index (V0001), given a
 * `note` column by V0004. The model is the final truth the replayed snapshots must equal.
 */
#[Table('shadow_gadgets')]
#[Index(['code'], unique: true)]
#[Trigger(ShadowGadgetTrigger::class)]
final class ShadowGadget
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;

    #[Column]
    public string $code;

    #[Column]
    public ?string $note = null;
}
