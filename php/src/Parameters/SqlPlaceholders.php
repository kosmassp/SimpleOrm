<?php

declare(strict_types=1);

namespace SimpleOrm\Parameters;

/**
 * Finds `@name` placeholders in SQL, ignoring string literals and comments
 * (§7.12/§7.13, mirrors dotnet/src/SimpleOrm/SqlPlaceholders.cs byte-for-byte in
 * behavior). A length-preserving mask turns literal and comment characters into
 * spaces so match positions in the mask are positions in the original SQL —
 * `occurrences()` and `toPdo()` rely on that to rewrite only real placeholders.
 */
final class SqlPlaceholders
{
    private const PATTERN = '/@([A-Za-z_][A-Za-z0-9_]*)/';

    /**
     * Distinct placeholder names (without the leading `@`), in first-occurrence
     * order, exact case as written — case folding is the caller's job
     * (`ParameterBinder` matches case-insensitively against args properties).
     *
     * @return list<string>
     */
    public static function find(string $sql): array
    {
        $masked = self::mask($sql);
        preg_match_all(self::PATTERN, $masked, $matches);

        $seen = [];
        $names = [];
        foreach ($matches[1] as $name) {
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
        $masked = self::mask($sql);
        preg_match_all(self::PATTERN, $masked, $matches, PREG_OFFSET_CAPTURE);

        $spans = [];
        foreach ($matches[0] as $index => $match) {
            $candidate = $matches[1][$index][0];
            if (strcasecmp($candidate, $name) === 0) {
                [$text, $offset] = $match;
                $spans[] = [$offset, strlen($text)];
            }
        }

        return $spans;
    }

    /** Rewrites every real `@name` to PDO's `:name`; literals and comments are untouched. */
    public static function toPdo(string $sql): string
    {
        $masked = self::mask($sql);
        preg_match_all(self::PATTERN, $masked, $matches, PREG_OFFSET_CAPTURE);

        $result = $sql;
        for ($i = count($matches[0]) - 1; $i >= 0; $i--) {
            [$text, $offset] = $matches[0][$i];
            $name = $matches[1][$i][0];
            $result = substr_replace($result, ':' . $name, $offset, strlen($text));
        }

        return $result;
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
