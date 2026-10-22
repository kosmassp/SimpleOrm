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
 * The multi-event, narrowed, **before** declaration of the live legs (ADR-0042, spec/dialects.md "Testing contract"):
 * fires on insert and on an update of `name` only — PostgreSQL has the form natively, SQLite splits it per event, SQL
 * Server refuses it (`DDL-005`: no BEFORE triggers).
 */
#[Table('trigger_probes')]
#[Trigger(ProbeBeforeTrigger::class)]
final class TriggerProbe
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
