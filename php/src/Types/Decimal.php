<?php

declare(strict_types=1);

namespace SimpleOrm\Types;

use InvalidArgumentException;
use Stringable;

/**
 * An exact decimal value (CODING-STANDARD §10): PHP has no decimal type and
 * floats would silently corrupt money. String-backed, canonical (no sign on
 * zero, no exponent, no thousands separators), compared by value. Arithmetic is
 * deliberately absent: the database does the math (§2 "real SQL first").
 */
final readonly class Decimal implements Stringable
{
    private function __construct(public string $value)
    {
    }

    public static function of(string|int $value): self
    {
        $text = is_int($value) ? (string) $value : trim($value);
        if (!preg_match('/^-?\d+(\.\d+)?$/', $text)) {
            throw new InvalidArgumentException("'{$text}' is not a decimal literal (digits with an optional fraction)");
        }

        if (preg_match('/^-0(\.0+)?$/', $text)) {
            $text = ltrim($text, '-');   // canonical zero
        }

        return new self($text);
    }

    public function equals(self $other): bool
    {
        return $this->normalized() === $other->normalized();
    }

    public function __toString(): string
    {
        return $this->value;
    }

    /** Value comparison ignores trailing fraction zeros: 19.90 equals 19.9. */
    private function normalized(): string
    {
        return str_contains($this->value, '.') ? rtrim(rtrim($this->value, '0'), '.') : $this->value;
    }
}
