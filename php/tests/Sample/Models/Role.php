<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToMany;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Session\Navigations;

/** Table `roles`. Mirrors dotnet/samples `Role`: the name column is explicit (`role_name`, renamed by V0004). */
#[Table('roles')]
#[Index(['name'], unique: true)]
final class Role extends BaseModel
{
    use Navigations;

    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column('role_name')]
    public string $name;

    #[ManyToMany(User::class, through: UserRole::class)]
    public private(set) array $users = [];
}
