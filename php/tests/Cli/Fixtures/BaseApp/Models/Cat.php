<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\BaseApp\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Table;

#[Table('cats')]
final class Cat extends Animal
{
    #[Column]
    public ?string $name = null;
}
