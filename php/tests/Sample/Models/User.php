<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToMany;
use SimpleOrm\Metadata\Attributes\OneToMany;
use SimpleOrm\Metadata\Attributes\OneToOne;
use SimpleOrm\Metadata\Attributes\Table;

/** Table `users` (STRICT). Key: `id`, database-generated. Mirrors dotnet/samples `User`; export pinned by conformance/entities/user.json. */
#[Table('users')]
#[Index(['email'], unique: true)]
#[Index(['displayName'])]
final class User extends BaseModel
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;

    #[Column]
    public string $email;

    /** Added by migration V0002; backfilled from `name` for pre-existing rows. */
    #[Column]
    public ?string $displayName = null;

    /** Populated only by the library (Level 2 loading — declaration-only in this port); never a column, never written. */
    #[OneToMany(Transaction::class, 'userId')]
    public private(set) array $transactions = [];

    /** Resolved through the `UserRole` link — declared, never inferred (ADR-0019). */
    #[ManyToMany(Role::class, through: UserRole::class)]
    public private(set) array $roles = [];

    /** The inverse side of the 1:1 — the FK (with its unique index) lives on `UserProfile`. */
    #[OneToOne('userId')]
    public private(set) ?UserProfile $profile = null;
}
