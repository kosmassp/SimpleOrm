<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Models\Tables;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToMany;
use SimpleOrm\Metadata\Attributes\Table;

/** Table `roles` (STRICT). Key: `id`, database-generated. */
#[Table('roles')]
#[Index(['name'], unique: true)]
final class Role extends BaseModel
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    /** Column renamed to `role_name` by migration V0004. */
    #[Column('role_name')]
    public string $name;

    /** The reverse side of `User::$roles`, through the same link (ADR-0019). Declaration-only in this port. */
    #[ManyToMany(User::class, through: UserRole::class)]
    public private(set) array $users = [];
}
