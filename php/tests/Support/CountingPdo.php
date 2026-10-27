<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PDO;
use PDOStatement;

/**
 * A real PDO connection that remembers what the session asked of it: the texts passed to `prepare()` (so a test can see
 * that a planned message reuses one prepared statement per distinct text, ADR-0044), how many transactions were begun
 * (an empty send begins none), and the attributes set on it (the command timeout reaching the connection, ADR-0047). It
 * decorates the driver call, never the database — the "no mocks" rule of CODING-STANDARD §7 stands: every statement still
 * runs on the real SQLite file.
 */
final class CountingPdo extends PDO
{
    /** @var list<string> */
    public array $prepared = [];

    public int $begun = 0;

    /** @var array<int, mixed> every attribute set on the connection after it opened, by attribute (the last value wins) */
    public array $attributes = [];

    public function setAttribute(int $attribute, mixed $value): bool
    {
        $this->attributes[$attribute] = $value;

        return parent::setAttribute($attribute, $value);
    }

    public function prepare(string $query, array $options = []): PDOStatement|false
    {
        $this->prepared[] = $query;

        return parent::prepare($query, $options);
    }

    public function beginTransaction(): bool
    {
        $this->begun++;

        return parent::beginTransaction();
    }

    /** How many times `$text` was prepared. */
    public function preparedCount(string $text): int
    {
        return count(array_filter($this->prepared, static fn (string $query): bool => $query === $text));
    }

    /** How many prepared texts start with `$prefix` (`insert into roles`). */
    public function preparedStartingWith(string $prefix): int
    {
        return count(array_filter($this->prepared, static fn (string $query): bool => str_starts_with($query, $prefix)));
    }
}
