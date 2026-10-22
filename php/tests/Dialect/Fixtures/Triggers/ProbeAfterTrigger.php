<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Triggers;

use SimpleOrm\Metadata\Attributes\Trigger;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/** The `after` twin {@see TriggerProbeAfter} links: insert, or an update of `name`. */
#[Trigger('trg_probe_after', TriggerTiming::After, [TriggerEvent::Insert, TriggerEvent::Update], 'insert into probe_logs (name) select n.name from {new: name} n;', updateOf: ['name'])]
final class ProbeAfterTrigger
{
}
