<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Models\Tables;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\Table;

/**
 * Table `user_profiles` (STRICT), the one-to-one fixture (ADR-0019 add.1): this
 * side holds the foreign key with a **unique index** (the database is what makes
 * a 1:1 a 1:1), so it declares an ordinary `#[ManyToOne]`; the inverse single
 * navigation lives on `User`.
 */
#[Table('user_profiles')]
#[Index(['userId'], unique: true)]
final class UserProfile extends BaseModel
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(User::class)]
    public int $userId;

    /** Populated only by the library (Level 2 loading). */
    #[ManyToOne('userId')]
    public private(set) ?User $user = null;

    #[Column]
    public ?string $bio = null;

    #[Column]
    public ?string $avatarUrl = null;
}
