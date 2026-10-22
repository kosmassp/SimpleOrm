<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models;

use SimpleOrm\Metadata\Attributes\Trigger;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/** The trigger of the shadow round trip: a declaration object, linked by {@see ShadowGadget}. The body never fires in the replay (no row is updated). */
#[Trigger('trg_shadow_gadgets', TriggerTiming::After, [TriggerEvent::Update], <<<'SQL'
    update shadow_gadgets set name = name where id in (select n.id from {new: id} n);
    SQL)]
final class ShadowGadgetTrigger
{
}
