<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use SimpleOrm\Errors\SimpleOrmException;

/**
 * The dialects this port ships, by the names the CLI's `--dialect` option and
 * the conformance `ast/` pins use (spec/dialects.md "Connection strings and the
 * CLI"): the one place a dialect name resolves to its implementation, so the
 * CLI and the AST runners never disagree about which dialects exist. Adding a
 * dialect is one `match` arm and one entry in {@see self::names()}.
 */
final class Dialects
{
    public const SQLITE = 'sqlite';

    private function __construct()
    {
    }

    /**
     * Every dialect name, in the order the reference's conformance runner lists
     * them: sqlite, then the Level 4 dialects as they are ported.
     *
     * @return list<string>
     */
    public static function names(): array
    {
        return [self::SQLITE];
    }

    /** A fresh dialect for a name (case-insensitive); an unknown name refuses before anything opens. */
    public static function create(string $name): Dialect
    {
        return match (strtolower(trim($name))) {
            self::SQLITE => new SqliteDialect(),
            default => throw new SimpleOrmException(
                'CLI',
                'dialect',
                "unknown dialect '{$name}'; this port ships " . implode(', ', self::names()),
            ),
        };
    }
}
