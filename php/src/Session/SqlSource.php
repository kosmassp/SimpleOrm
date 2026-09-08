<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Errors\SimpleOrmException;

/**
 * Where a registry entry's SQL comes from (§7.5), mirroring
 * dotnet/src/SimpleOrm/QueryRegistry.cs's `SqlSource`. `inline()` carries the
 * SQL text verbatim; `fromFile()` reads a `.sql` file from disk — a missing
 * file is `QRY-003`. Unlike the C# reference's `Lazy<string>` (deferred until
 * first use, cached), the PHP port's registry entries are built fresh by a
 * static factory method on every call (CODING-STANDARD §10: PHP has no `new` in
 * static property initializers), so there is no separate "declaration" moment
 * to defer past — reading eagerly here is observably identical.
 */
final readonly class SqlSource
{
    private function __construct(
        public string $sql,
        public string $description,
    ) {
    }

    /** The SQL verbatim; the description is a whitespace-collapsed prefix, for error messages. */
    public static function inline(string $sql): self
    {
        $head = strlen($sql) <= 60 ? $sql : substr($sql, 0, 60) . '…';
        $words = array_values(array_filter(preg_split('/\s+/', $head) ?: [], static fn (string $w): bool => $w !== ''));

        return new self($sql, 'inline: ' . implode(' ', $words));
    }

    /** Reads the SQL from a file; a missing file is `QRY-003` (named by its path). */
    public static function fromFile(string $path): self
    {
        if (!is_file($path)) {
            throw new SimpleOrmException('QRY-003', $path, "no file at '{$path}'");
        }

        $sql = file_get_contents($path);
        if ($sql === false) {
            throw new SimpleOrmException('QRY-003', $path, "could not read '{$path}'");
        }

        return new self($sql, $path);
    }
}
