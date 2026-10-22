<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Trigger\RoleAuditTrigger;

use SimpleOrm\Migrations\TriggerActions;
use SimpleOrm\Migrations\TriggerMigration;
use SimpleOrm\Sample\Models\Tables\Role;
use SimpleOrm\Sample\Models\Triggers\RoleAuditTrigger;

/**
 * The trigger's initial creation, rendered from the entity's linked declaration (drop if present, then create; ADR-0042).
 * No down: the runner derives the rollback — the guard, the drop — from the trigger snapshot (ADR-0018).
 */
final class V0013_CreateRoleAuditTrigger extends TriggerMigration
{
    public function entityClass(): string
    {
        return Role::class;
    }

    public function triggerClass(): string
    {
        return RoleAuditTrigger::class;
    }

    public function action(TriggerActions $actions): void
    {
        $actions->createTrigger();
    }
}
