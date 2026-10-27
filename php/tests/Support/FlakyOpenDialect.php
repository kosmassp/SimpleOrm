<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PDO;
use PDOException;
use SimpleOrm\Dialect\Dialect;
use Throwable;

/**
 * A real dialect whose open can be made to fail (ADR-0047, spec/dialects.md "Testing contract"): it counts every
 * `createConnection()` call, fails the first `$failures` of them with a `PDOException` of its own — one it calls transient
 * or not, as asked — and then opens the real connection of the dialect it wraps. A failure it did not throw is judged by
 * the wrapped dialect, so around `PostgresDialect` it is the attempt counter of the live legs. It decorates the dialect
 * seam, never the database (CODING-STANDARD §7).
 */
final class FlakyOpenDialect extends DelegatingDialect
{
    /** How many times the session asked for a connection. */
    public int $attempts = 0;

    /** @var list<Throwable> the failures this decorator raised, in order */
    public array $thrown = [];

    public function __construct(Dialect $inner, private readonly int $failures = 0, private readonly bool $transient = true)
    {
        parent::__construct($inner);
    }

    public function createConnection(string $connectionString): PDO
    {
        $this->attempts++;
        if ($this->attempts <= $this->failures) {
            $failure = new PDOException("simulated open failure #{$this->attempts}");
            $this->thrown[] = $failure;

            throw $failure;
        }

        return $this->inner->createConnection($connectionString);
    }

    public function isTransient(Throwable $failure): bool
    {
        return in_array($failure, $this->thrown, true) ? $this->transient : $this->inner->isTransient($failure);
    }
}
