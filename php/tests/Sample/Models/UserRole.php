<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\Table;

/**
 * Table `user_roles`: composite natural key (`user_id`, `role_id`) — the
 * composite-key fixture (§7.4) and the many-to-many link between users and roles.
 */
#[Table('user_roles')]
#[Index(['roleId'])]
final class UserRole extends BaseModel
{
    #[Key]
    #[Column]
    #[ForeignKey(User::class)]
    public int $userId;

    #[Key]
    #[Column]
    #[ForeignKey(Role::class)]
    public int $roleId;

    /** Added by migration V0005. */
    #[Column]
    public ?string $grantedBy = null;

    #[ManyToOne('userId')]
    public private(set) ?User $user = null;

    #[ManyToOne('roleId')]
    public private(set) ?Role $role = null;
}
