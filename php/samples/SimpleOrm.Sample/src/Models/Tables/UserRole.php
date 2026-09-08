<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Models\Tables;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\Table;

/**
 * Table `user_roles` (STRICT). Composite key (`user_id`, `role_id`), neither
 * part database-generated: the fixture for composite-key support (§7.4) and
 * the many-to-many link between users and roles. `createdAtUtc` from the base
 * doubles as the assignment timestamp.
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

    /** Populated only by the library (Level 2 loading); no public setter, so it can never disagree with `userId`. */
    #[ManyToOne('userId')]
    public private(set) ?User $user = null;

    /** Populated only by the library (Level 2 loading); no public setter, so it can never disagree with `roleId`. */
    #[ManyToOne('roleId')]
    public private(set) ?Role $role = null;
}
