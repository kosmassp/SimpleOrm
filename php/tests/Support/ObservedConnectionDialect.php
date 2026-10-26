<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PDO;
use SimpleOrm\Dialect\Dialect;

/**
 * The SQLite dialect with two test-only knobs (CODING-STANDARD §7, a {@see DelegatingDialect}): its connection is a
 * {@see CountingPdo}, so a test can read what the session prepared and how many transactions it began, and its
 * `maxParametersPerMessage()` can be set, so the plan's rule 3 (the dialect's parameter cap, SQL Server's 2098) is
 * exercised on the real SQLite file without an SQL Server.
 */
final class ObservedConnectionDialect extends DelegatingDialect
{
    public ?CountingPdo $connection = null;

    public function __construct(Dialect $inner, private readonly ?int $parameterCap = null)
    {
        parent::__construct($inner);
    }

    public function createConnection(string $connectionString): PDO
    {
        $pdo = new CountingPdo('sqlite:' . $connectionString);
        $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
        $pdo->setAttribute(PDO::ATTR_EMULATE_PREPARES, false);
        $pdo->setAttribute(PDO::ATTR_STRINGIFY_FETCHES, false);
        $pdo->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_ASSOC);
        $this->connection = $pdo;

        return $pdo;
    }

    public function maxParametersPerMessage(): ?int
    {
        return $this->parameterCap ?? $this->inner->maxParametersPerMessage();
    }
}
