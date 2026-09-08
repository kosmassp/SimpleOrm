<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

/**
 * A registered command: SQL bound to its args type; execution returns the
 * affected-row count (§6, §7.5). Mirrors dotnet's `Command<TArgs>` (see
 * {@see Query} for the class-string adaptation, CODING-STANDARD §10).
 */
final class Command
{
    private function __construct(
        public readonly string $argsType,
        public readonly SqlSource $source,
    ) {
    }

    public static function inline(string $argsType, string $sql): self
    {
        return new self($argsType, SqlSource::inline($sql));
    }

    public static function fromFile(string $argsType, string $path): self
    {
        return new self($argsType, SqlSource::fromFile($path));
    }
}
