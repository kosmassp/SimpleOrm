<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Trigger;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/**
 * The fixture trigger (Level 4, ADR-0042): after every update of a role, one `role_changes` row per updated row —
 * one neutral body, three renderings. A declaration object: it has no columns and no key, `Role` links it.
 */
#[Trigger('trg_roles_audit', TriggerTiming::After, [TriggerEvent::Update], <<<'SQL'
    insert into role_changes (role_id, role_name, changed_at)
    select n.id, n.role_name, {now}
    from {new: id, role_name} n;
    SQL)]
final class RoleAuditTrigger
{
}
