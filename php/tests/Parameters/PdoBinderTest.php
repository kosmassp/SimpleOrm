<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Parameters;

use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * Pins the explicit PDO typing that keeps numeric comparisons honest where
 * SQLite has no declared affinity to coerce with (CODING-STANDARD §5/§10): a
 * bare `PDOStatement::execute([...])` would send every value as text, and a
 * TEXT "0" sorts after every INTEGER.
 */
final class PdoBinderTest extends TestCase
{
    private TempDatabase $fixture;

    private PDO $connection;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->connection = (new SqliteDialect())->createConnection($this->fixture->connectionString());
    }

    protected function tearDown(): void
    {
        unset($this->connection);
        $this->fixture->delete();
    }

    #[Test]
    public function integers_bind_as_integer_not_text(): void
    {
        self::assertSame(['t' => 'integer', 'eq' => 1], $this->row('select typeof(:v) as t, :v = 5 as eq', ['v' => 5]));
    }

    #[Test]
    public function numeric_filter_over_an_affinity_less_aggregate_matches(): void
    {
        $this->connection->exec('create table things (id INTEGER PRIMARY KEY) STRICT');
        $this->connection->exec('insert into things (id) values (1), (2)');
        $this->connection->exec('create view thing_counts as select count(*) as n from things');

        self::assertSame(['n' => 2], $this->row('select n from thing_counts where n > :floor', ['floor' => 0]));
    }

    #[Test]
    public function null_binds_as_sql_null(): void
    {
        self::assertSame(['t' => 'null'], $this->row('select typeof(:v) as t', ['v' => null]));
    }

    #[Test]
    public function booleans_bind_as_integer(): void
    {
        self::assertSame(['t' => 'integer', 'v' => 1], $this->row('select typeof(:v) as t, :v as v', ['v' => true]));
    }

    #[Test]
    public function numeric_looking_strings_stay_text(): void
    {
        self::assertSame(['t' => 'text'], $this->row('select typeof(:v) as t', ['v' => '5']));
    }

    /** @param array<string, mixed> $parameters @return array<string, mixed> */
    private function row(string $sql, array $parameters): array
    {
        $statement = $this->connection->prepare($sql);
        PdoBinder::bindAndExecute($statement, $parameters);
        $row = $statement->fetch(PDO::FETCH_ASSOC);
        self::assertIsArray($row);

        return $row;
    }
}
