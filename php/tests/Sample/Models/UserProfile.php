<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\Table;

/** Table `user_profiles` — the one-to-one fixture (ADR-0019 add.1): this side holds the FK with a unique index. */
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

    #[ManyToOne('userId')]
    public private(set) ?User $user = null;

    #[Column]
    public ?string $bio = null;

    #[Column]
    public ?string $avatarUrl = null;
}
