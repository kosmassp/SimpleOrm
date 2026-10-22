<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use Closure;
use SimpleOrm\Errors\SimpleOrmException;

/**
 * The trigger body's token parser and renderer (Level 4, ADR-0042, spec/metadata-model.md "Triggers"): the one
 * implementation every dialect, the loader's `MAP-029` checks, and the snapshot normalization share
 * (CODING-STANDARD §8). A body is set-based SQL with exactly three tokens — `{new: a, b}`, `{old: a, b}`, `{now}`;
 * any other `{…}` is a malformed token. Braces inside a single-quoted SQL string literal are text, not tokens
 * (a JSON literal in a body needs a way to say a brace, and the grammar has no escape).
 */
final class TriggerBody
{
    private function __construct()
    {
    }

    /**
     * The body as an ordered list of literal text (string) and tokens. Every malformed or unknown token adds one
     * problem to `$problems` and is kept as text, so one pass reports them all.
     *
     * @param list<string> $problems appended to, by reference
     * @return list<string|TriggerToken>
     */
    public static function parse(string $sql, array &$problems): array
    {
        $segments = [];
        $buffer = '';
        $length = strlen($sql);
        $lineComment = false;
        $i = 0;
        while ($i < $length) {
            $char = $sql[$i];
            if ($char === "'") {
                $end = self::literalEnd($sql, $i);
                $buffer .= substr($sql, $i, $end - $i);
                $i = $end;

                continue;
            }

            if ($char === '-' && $i + 1 < $length && $sql[$i + 1] === '-' && !$lineComment) {
                // Snapshots and exports collapse whitespace, which would fold the rest of the body into the comment.
                $problems[] = 'a body cannot carry a -- line comment (whitespace is collapsed in snapshots); use /* … */';
                $lineComment = true;
            }

            // A token is `{…}` with no brace inside; a lone `{` or `}` is text (spec/metadata-model.md "Triggers").
            $close = $char === '{' ? strpos($sql, '}', $i) : false;
            $open = $char === '{' ? strpos($sql, '{', $i + 1) : false;
            if ($close === false || ($open !== false && $open < $close)) {
                $buffer .= $char;
                $i++;

                continue;
            }

            $inner = substr($sql, $i + 1, $close - $i - 1);
            $token = self::parseToken($inner, $problems);
            if ($token === null) {
                $buffer .= substr($sql, $i, $close - $i + 1);
            } else {
                if ($buffer !== '') {
                    $segments[] = $buffer;
                    $buffer = '';
                }

                $segments[] = $token;
            }

            $i = $close + 1;
        }

        if ($buffer !== '') {
            $segments[] = $buffer;
        }

        return $segments;
    }

    /**
     * The body whitespace-collapsed and trimmed, its tokens in their canonical spelling and **unexpanded** — what the
     * export, the snapshot, and the diff compare (layout is not schema).
     */
    public static function normalize(string $sql): string
    {
        $problems = [];
        $text = '';
        foreach (self::parse($sql, $problems) as $segment) {
            $text .= $segment instanceof TriggerToken
                ? $segment->canonical()
                : (string) preg_replace('/\s+/u', ' ', $segment);
        }

        return trim($text);
    }

    /**
     * Expands the trimmed body for a dialect: `$row` renders `{new: …}`/`{old: …}` (kind, columns), `$now` renders
     * `{now}`; the text between tokens is verbatim. A malformed token is `MAP-029` (a data-driven declaration reaches
     * a dialect without passing the loader).
     *
     * @param Closure(TriggerTokenKind, list<string>): string $row
     */
    public static function expand(string $sql, string $triggerName, Closure $row, string $now): string
    {
        $problems = [];
        $segments = self::parse(trim($sql), $problems);
        if ($problems !== []) {
            throw new SimpleOrmException('MAP-029', $triggerName, 'the trigger body has a malformed token: ' . implode('; ', $problems));
        }

        $out = '';
        foreach ($segments as $segment) {
            $out .= match (true) {
                is_string($segment) => $segment,
                $segment->kind === TriggerTokenKind::Now => $now,
                default => $row($segment->kind, $segment->columns),
            };
        }

        return $out;
    }

    /** @param list<string> $problems */
    private static function parseToken(string $inner, array &$problems): ?TriggerToken
    {
        $trimmed = trim($inner);
        if (strcasecmp($trimmed, 'now') === 0) {
            return new TriggerToken(TriggerTokenKind::Now);
        }

        if (preg_match('/^(new|old)\s*(?::(.*))?$/is', $trimmed, $matches) !== 1) {
            $problems[] = "unknown token '{{$inner}}': the tokens are {new: columns}, {old: columns} and {now}";

            return null;
        }

        $kind = strtolower($matches[1]) === 'new' ? TriggerTokenKind::New : TriggerTokenKind::Old;
        if (!isset($matches[2])) {
            $problems[] = "token '{{$inner}}' has no column list: write {{$kind->value}: column, column}";

            return null;
        }

        $columns = [];
        foreach (explode(',', $matches[2]) as $part) {
            $column = trim($part);
            if ($column === '') {
                $problems[] = "token '{{$inner}}' has an empty column entry";

                return null;
            }

            $columns[] = $column;
        }

        return new TriggerToken($kind, $columns);
    }

    /** The offset just past the string literal that opens at `$start` (`''` escapes a quote); the end of input when unterminated. */
    private static function literalEnd(string $sql, int $start): int
    {
        $length = strlen($sql);
        $i = $start + 1;
        while ($i < $length) {
            if ($sql[$i] === "'") {
                if ($i + 1 < $length && $sql[$i + 1] === "'") {
                    $i += 2;

                    continue;
                }

                return $i + 1;
            }

            $i++;
        }

        return $length;
    }
}
