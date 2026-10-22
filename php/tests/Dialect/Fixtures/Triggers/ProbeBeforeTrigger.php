<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Triggers;

use SimpleOrm\Metadata\Attributes\Trigger;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/** The multi-event, narrowed, `before` declaration {@see TriggerProbe} links: insert, or an update of `name`. */
#[Trigger('trg_probe_before', TriggerTiming::Before, [TriggerEvent::Insert, TriggerEvent::Update], 'insert into probe_logs (name) select n.name from {new: name} n;', updateOf: ['name'])]
final class ProbeBeforeTrigger
{
}
