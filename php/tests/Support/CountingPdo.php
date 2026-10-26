<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PDO;
use PDOStatement;

/**
 * A real PDO connection that remembers what the session asked of it: the texts passed to `prepare()` (so a test can see
 * that a planned message reuses one prepared statement per distinct text, ADR-0044) and how many transactions were begun
 * (an empty send begins none). It decorates the driver call, never the database — the "no mocks" rule of CODING-STANDARD §7
 * stands: every statement still runs on the real SQLite file.
 */
final class CountingPdo extends PDO
{
    /** @var list<string> */
    public array $prepared = [];

    public int $begun = 0;

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
