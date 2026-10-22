<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;

/**
 * Table `role_changes` (Level 4, ADR-0042): the audit table the `RoleAuditTrigger` writes — one row per updated role.
 * Not an audit-hook model (no `BaseModel`): the trigger fills `changed_at` itself with its `{now}` token.
 */
#[Table('role_changes')]
final class RoleChange
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public int $roleId;

    #[Column]
    public string $roleName;

    #[Column]
    public DateTimeImmutable $changedAt;
}
