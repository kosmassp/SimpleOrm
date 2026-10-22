<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToMany;
use SimpleOrm\Metadata\Attributes\RenamedFrom;
use SimpleOrm\Metadata\Attributes\SoftDelete;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\Trigger;
use SimpleOrm\Session\Navigations;

/** Table `roles`. Mirrors dotnet/samples `Role`: the name column is explicit (`role_name`, renamed by V0004). Links the audit trigger (ADR-0042). */
#[Table('roles')]
#[Index(['name'], unique: true)]
#[Trigger(RoleAuditTrigger::class)]
final class Role extends BaseModel
{
    use Navigations;

    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column('role_name')]
    #[RenamedFrom('name')]
    public string $name;

    /** The soft-delete flag (spec/hooks.md, migration V0011): a delete sets it; reads never filter on it. */
    #[Column('is_deleted')]
    #[SoftDelete]
    public bool $isDeleted = false;

    #[ManyToMany(User::class, through: UserRole::class)]
    public private(set) array $users = [];
}
