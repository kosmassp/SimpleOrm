<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\TriggerApp\Models;

use SimpleOrm\Metadata\Attributes\Trigger;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/** One trigger class linked by two entities: the MAP-029 scan conflict (ADR-0042) a lone load cannot see. */
#[Trigger('trg_shared', TriggerTiming::After, [TriggerEvent::Update], 'select 1;')]
final class SharedTrigger
{
}
