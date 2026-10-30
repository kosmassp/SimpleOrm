<?php

declare(strict_types=1);

namespace SimpleOrm\Parameters;

/**
 * Finds `@name` placeholders in SQL, ignoring string literals and comments
 * (§7.12/§7.13, mirrors dotnet/src/SimpleOrm/SqlPlaceholders.cs byte-for-byte in
 * behavior). A length-preserving mask turns literal and comment characters into
 * spaces so match positions in the mask are positions in the original SQL —
 * `occurrences()` and `toPdo()` rely on that to rewrite only real placeholders.
 *
 * The scan is a pure function of the text, so it runs once per text (ADR-0048 item 2): the matches and the PDO form are
 * kept by the SQL text itself — the text is the whole input, so equal texts can never need different answers — in a
 * bounded table ({@see MAX_CACHED_TEXTS}, emptied when full; CODING-STANDARD §10).
 */
final class SqlPlaceholders
{
    /**
     * A placeholder is `@name`; the lookbehind keeps SQL Server's system functions (`@@rowcount`, `@@identity`) out of it —
     * their second `@` is not the start of a name. Without it `select @@rowcount` became `@:rowcount` under {@see toPdo()}.
     */
    private const PATTERN = '/(?<!@)@([A-Za-z_][A-Za-z0-9_]*)/';

    /** How many distinct texts the scan remembers; a caller generating texts without bound only empties the table. */
    public const int MAX_CACHED_TEXTS = 1024;

    /** @var array<string, list<array{0: string, 1: int, 2: int}>> per text: every real match as (name, offset, length) */
    private static array $matches = [];

    /** @var array<string, string> per text: its PDO form */
    private static array $pdo = [];

    private static int $scans = 0;

    /** How many texts have been scanned in this process — the cache's test hook. @internal */
    public static function scanCount(): int
    {
        return self::$scans;
    }

    /**
     * Distinct placeholder names (without the leading `@`), in first-occurrence
     * order, exact case as written — case folding is the caller's job
     * (`ParameterBinder` matches case-insensitively against args properties).
     *
     * @return list<string>
     */
    public static function find(string $sql): array
    {
        $seen = [];
        $names = [];
        foreach (self::matches($sql) as [$name]) {
            if (!isset($seen[$name])) {
                $seen[$name] = true;
                $names[] = $name;
            }
        }

        return $names;
    }

    /**
     * The (start, length) spans of every real occurrence of one placeholder,
     * matched case-insensitively — lookalikes inside string literals and
     * comments are not placeholders, for rewriting exactly as for detection.
     *
     * @return list<array{0: int, 1: int}>
     */
    public static function occurrences(string $sql, string $name): array
    {
        $spans = [];
        foreach (self::matches($sql) as [$candidate, $offset, $length]) {
            if (strcasecmp($candidate, $name) === 0) {
                $spans[] = [$offset, $length];
            }
        }

        return $spans;
    }

    /** Rewrites every real `@name` to PDO's `:name`; literals and comments are untouched. */
    public static function toPdo(string $sql): string
    {
        if (isset(self::$pdo[$sql])) {
            return self::$pdo[$sql];
        }

        $result = $sql;
        $matches = self::matches($sql);
        for ($i = count($matches) - 1; $i >= 0; $i--) {
            [$name, $offset, $length] = $matches[$i];
            $result = substr_replace($result, ':' . $name, $offset, $length);
        }

        if (count(self::$pdo) >= self::MAX_CACHED_TEXTS) {
            self::$pdo = [];
        }

        return self::$pdo[$sql] = $result;
    }

    /**
     * Renames every real `@name` to `@<prefix>name`, literals and comments untouched (ADR-0044): the SQL Server
     * realization of a planned message joins several statements into one text and renames each statement's
     * placeholders apart (`@p<i>_name` for the i-th statement) so the bound names cannot collide.
     */
    public static function prefixed(string $sql, string $prefix): string
    {
        $result = $sql;
        $matches = self::matches($sql);
        for ($i = count($matches) - 1; $i >= 0; $i--) {
            [$name, $offset, $length] = $matches[$i];
            $result = substr_replace($result, '@' . $prefix . $name, $offset, $length);
        }

        return $result;
    }

    /**
     * Every real placeholder of the text, in order, as (name, offset, length) — scanned once per text.
     *
     * @return list<array{0: string, 1: int, 2: int}>
     */
    private static function matches(string $sql): array
    {
        if (isset(self::$matches[$sql])) {
            return self::$matches[$sql];
        }

        self::$scans++;
        preg_match_all(self::PATTERN, self::mask($sql), $found, PREG_OFFSET_CAPTURE);
        $matches = [];
        foreach ($found[0] as $index => [$text, $offset]) {
            $matches[] = [$found[1][$index][0], $offset, strlen($text)];
        }

        if (count(self::$matches) >= self::MAX_CACHED_TEXTS) {
            self::$matches = [];
        }

        return self::$matches[$sql] = $matches;
    }

    /**
     * Length-preserving mask: string-literal and comment characters become
     * spaces, so match positions in the mask are positions in the original SQL.
     */
    private static function mask(string $sql): string
    {
        $out = '';
        $length = strlen($sql);
        $i = 0;
        while ($i < $length) {
            $c = $sql[$i];
            if ($c === "'") {
                // String literal; '' is the escaped quote.
                $out .= ' ';
                $i++;
                while ($i < $length) {
                    if ($sql[$i] === "'") {
                        if ($i + 1 < $length && $sql[$i + 1] === "'") {
                            $out .= '  ';
                            $i += 2;
                            continue;
                        }

                        $out .= ' ';
                        $i++;
                        break;
                    }

                    $out .= ' ';
                    $i++;
                }
            } elseif ($c === '-' && $i + 1 < $length && $sql[$i + 1] === '-') {
                while ($i < $length && $sql[$i] !== "\n") {
                    $out .= ' ';
                    $i++;
                }
            } elseif ($c === '/' && $i + 1 < $length && $sql[$i + 1] === '*') {
                $out .= '  ';
                $i += 2;
                while ($i + 1 < $length && !($sql[$i] === '*' && $sql[$i + 1] === '/')) {
                    $out .= ' ';
                    $i++;
                }

                if ($i + 1 < $length) {
                    $out .= '  ';
                } elseif ($i < $length) {
                    $out .= ' ';
                }

                $i = min($i + 2, $length);
            } else {
                $out .= $c;
                $i++;
            }
        }

        return $out;
    }
}
