<?php

declare(strict_types=1);

namespace SimpleOrm\Json;

use InvalidArgumentException;
use JsonSerializable;

/**
 * The one JSON writer for conformance artifacts (EntityMap exports, schema
 * snapshots): byte-identical to the C# reference's System.Text.Json output —
 * two-space indent, insertion-ordered keys, `[]` for empty lists, and the
 * default STJ escaper (`"` `\` `<` `>` `&` `'` `+` and non-ASCII as uppercase
 * `\uXXXX`; the short escapes for the common controls). PHP's `json_encode`
 * cannot produce this shape, and two writers would drift (CODING-STANDARD §8).
 *
 * Documents are PHP arrays: a list (sequential integer keys) is a JSON array, any
 * other array is a JSON object. An empty array is `[]`; use `Obj::empty()`-style
 * wrappers only if a case ever needs `{}` — none does today.
 */
final class CanonicalWriter
{
    public static function write(array $document): string
    {
        return self::value($document, 0);
    }

    private static function value(mixed $value, int $depth): string
    {
        return match (true) {
            $value === null => 'null',
            $value === true => 'true',
            $value === false => 'false',
            is_int($value) => (string) $value,
            is_float($value) => self::float($value),
            is_string($value) => self::string($value),
            $value instanceof JsonSerializable => self::value($value->jsonSerialize(), $depth),
            is_array($value) => array_is_list($value) ? self::list($value, $depth) : self::object($value, $depth),
            default => throw new InvalidArgumentException('cannot write a ' . get_debug_type($value) . ' as canonical JSON'),
        };
    }

    /** @param list<mixed> $items */
    private static function list(array $items, int $depth): string
    {
        if ($items === []) {
            return '[]';
        }

        $inner = str_repeat('  ', $depth + 1);
        $lines = array_map(static fn (mixed $item): string => $inner . self::value($item, $depth + 1), $items);

        return "[\n" . implode(",\n", $lines) . "\n" . str_repeat('  ', $depth) . ']';
    }

    /** @param array<string, mixed> $members */
    private static function object(array $members, int $depth): string
    {
        if ($members === []) {
            return '{}';
        }

        $inner = str_repeat('  ', $depth + 1);
        $lines = [];
        foreach ($members as $key => $member) {
            $lines[] = $inner . self::string((string) $key) . ': ' . self::value($member, $depth + 1);
        }

        return "{\n" . implode(",\n", $lines) . "\n" . str_repeat('  ', $depth) . '}';
    }

    private static function float(float $value): string
    {
        $text = (string) $value;

        return str_contains($text, '.') || str_contains($text, 'E') ? $text : $text . '.0';
    }

    /** The STJ default escaper: short escapes for the common controls, `\uXXXX` (uppercase) for everything else it guards. */
    private static function string(string $value): string
    {
        $out = '"';
        $length = strlen($value);
        for ($i = 0; $i < $length; $i++) {
            $byte = $value[$i];
            $code = ord($byte);
            if ($code >= 0x80) {
                // A UTF-8 sequence: decode one code point and escape it as UTF-16 units.
                $sequenceLength = $code >= 0xF0 ? 4 : ($code >= 0xE0 ? 3 : 2);
                $codePoint = mb_ord(substr($value, $i, $sequenceLength), 'UTF-8');
                $i += $sequenceLength - 1;
                if ($codePoint > 0xFFFF) {
                    $codePoint -= 0x10000;
                    $out .= sprintf('\\u%04X\\u%04X', 0xD800 + ($codePoint >> 10), 0xDC00 + ($codePoint & 0x3FF));
                } else {
                    $out .= sprintf('\\u%04X', $codePoint);
                }

                continue;
            }

            $out .= match ($byte) {
                '"' => '\\u0022',
                '\\' => '\\\\',
                "\n" => '\\n',
                "\r" => '\\r',
                "\t" => '\\t',
                "\x08" => '\\b',
                "\x0C" => '\\f',
                '<' => '\\u003C',
                '>' => '\\u003E',
                '&' => '\\u0026',
                "'" => '\\u0027',
                '+' => '\\u002B',
                default => $code < 0x20 || $code === 0x7F ? sprintf('\\u%04X', $code) : $byte,
            };
        }

        return $out . '"';
    }
}
