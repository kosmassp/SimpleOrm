<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use SimpleOrm\Session\Command;

/**
 * The args of the fixture registry command `SetDisplayName` (spec/session.md "The step scripts"): `update users set
 * display_name = @DisplayName where id = @Id`, `Id` an int64 and `DisplayName` a nullable string, declared natively in
 * every runner. The placeholders match these properties case-insensitively, as they do for any registry command.
 */
final readonly class SetDisplayNameArgs
{
    public function __construct(
        public int $id,
        public ?string $displayName,
    ) {
    }

    public static function command(): Command
    {
        return Command::inline(self::class, 'update users set display_name = @DisplayName where id = @Id');
    }
}
