<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Triggers;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\Trigger;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/**
 * The same multi-event, narrowed declaration with timing `after` — the form SQL Server has (statement-level, the
 * update-of narrowing emulated by an `if not (update(…)) return;` prologue), and every dialect accepts (ADR-0042).
 */
#[Table('trigger_probe_afters')]
#[Trigger(ProbeAfterTrigger::class)]
final class TriggerProbeAfter
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name = '';

    #[Column]
    public ?string $note = null;
}
