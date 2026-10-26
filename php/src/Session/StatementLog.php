<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use Closure;
use DateTimeImmutable;
use SimpleOrm\Parameters\BinaryParameter;
use SimpleOrm\Types\Iso8601;
use Stringable;

use function array_map;
use function bin2hex;
use function implode;
use function is_array;
use function is_bool;
use function is_float;
use function is_int;
use function is_string;
use function str_replace;
use function strlen;
use function substr;

/**
 * The statement log's one funnel (Level 4 item 4, ADR-0045, spec/session.md "Statement log"): every place that sends a
 * statement to the database — the session's reads and writes, the migration runner, SchemaGuard, the shadow and the
 * force-sync — calls {@see self::write()} as the statement is sent (after it is bound, before it executes), and it writes
 * exactly one line, `<name>: <sql>` followed by the parameter names in bind order in square brackets, or with values on
 * `[@a = 'x', @b = 7]`, to the application's sink. Unset (`DbOptions::$log` null) nothing is formatted and nothing is
 * written: the Level 0–3 session exactly. Values are written only when asked (`DbOptions::$logParameterValues`) — they may
 * be personal data — in one culture-neutral form: a string single-quoted with `'` doubled, `null`, `true`/`false`, bytes as
 * `0x` + the first 16 as hex and the length, an instant as ISO-8601 text, an array in brackets, numbers invariant.
 *
 * The SQL is logged in the library's `@name` placeholder form (PDO sees `:name`; `SqlPlaceholders::toPdo()` is only the
 * rewrite), after IN-list expansion and the dialect's rendering. A sink that throws fails the call like any exception —
 * nothing here catches it (CODING-STANDARD §10).
 */
final readonly class StatementLog
{
    /**
     * @param (Closure(string): void)|null $sink
     * @param string|null $name when set, every line is written under this name whatever the caller passes: how the shadow
     *        and the force-sync run the migration runner's statements as `shadow` and `sync`
     */
    public function __construct(
        private ?Closure $sink = null,
        private bool $values = false,
        private ?string $name = null,
    ) {
    }

    /** The same log writing every line under `$name` (`shadow`, `sync`). */
    public function named(string $name): self
    {
        return new self($this->sink, $this->values, $name);
    }

    /** The log that writes nothing — what a component without a session defaults to. */
    public static function off(): self
    {
        return new self();
    }

    public function enabled(): bool
    {
        return $this->sink !== null;
    }

    /**
     * Writes the one line of a statement being sent.
     *
     * @param string $sql the text as sent, placeholders in `@name` form
     * @param array<string, mixed> $parameters the bound values by name (no prefix), in bind order
     */
    public function write(string $name, string $sql, array $parameters = []): void
    {
        if ($this->sink === null) {
            return;
        }

        ($this->sink)(self::line($this->name ?? $name, $sql, $parameters, $this->values));
    }

    /**
     * @param array<string, mixed> $parameters
     * @internal exposed for the formatter's own tests
     */
    public static function line(string $name, string $sql, array $parameters, bool $values): string
    {
        $line = $name . ': ' . $sql;
        if ($parameters === []) {
            return $line;
        }

        $items = [];
        foreach ($parameters as $parameter => $value) {
            $items[] = $values ? "@{$parameter} = " . self::value($value) : "@{$parameter}";
        }

        return $line . ' [' . implode(', ', $items) . ']';
    }

    /** One bound value in the fixed, culture-neutral form of the spec. */
    public static function value(mixed $value): string
    {
        return match (true) {
            $value === null => 'null',
            is_bool($value) => $value ? 'true' : 'false',
            is_int($value) => (string) $value,
            is_float($value) => (string) $value,   // PHP 8 renders floats independently of the locale
            is_string($value) => "'" . str_replace("'", "''", $value) . "'",
            $value instanceof BinaryParameter => self::bytes($value->bytes),
            $value instanceof DateTimeImmutable => Iso8601::format($value),
            is_array($value) => '[' . implode(', ', array_map(self::value(...), $value)) . ']',
            $value instanceof Stringable => (string) $value,   // a value object such as Decimal: a number, invariant
            default => "'" . str_replace("'", "''", get_debug_type($value)) . "'",
        };
    }

    private static function bytes(string $bytes): string
    {
        return '0x' . bin2hex(substr($bytes, 0, 16)) . ' (' . strlen($bytes) . ' bytes)';
    }
}
