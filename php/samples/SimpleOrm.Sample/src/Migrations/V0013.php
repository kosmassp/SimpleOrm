<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Sample\Migrations\Table\RoleChange\V0013_CreateRoleChanges;
use SimpleOrm\Sample\Migrations\Trigger\RoleAuditTrigger\V0013_CreateRoleAuditTrigger;

/** ADR-0042: the audit table, then the trigger that writes it (a trigger's relation must exist, so trigger steps compose last). */
final class V0013 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version
            ->apply(V0013_CreateRoleChanges::class)
            ->apply(V0013_CreateRoleAuditTrigger::class);
    }
}
