<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use LogicException;
use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Session\ResultKind;
use SimpleOrm\Session\ResultReader;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * What the session reads from an executed statement (ADR-0044, spec/dialects.md "Reading results", Clarification 24): the
 * first column of the first row of the next result that has columns, a reported count as an integer however the driver
 * spells it, and never zero for an absent one. Run on a real SQLite statement; `pdo_sqlite` has no multiple
 * rowsets, so the stepping across several statements (`scalar()` after a column-less result, `drain()`) is the SQL Server
 * leg's to prove (`SqlServerLiveTest`).
 */
final class ResultReaderTest extends TestCase
{
    private TempDatabase $fixture;

    private PDO $pdo;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->pdo = (new SqliteDialect())->createConnection($this->fixture->connectionString());
        $this->pdo->exec('create table t (id integer primary key, n integer not null)');
    }

    protected function tearDown(): void
    {
        unset($this->pdo);
        $this->fixture->delete();
    }

    #[Test]
    public function a_key_is_the_first_column_of_the_first_row(): void
    {
        $statement = $this->pdo->prepare('insert into t (n) values (5) returning id, n');
        $statement->execute();

        self::assertSame(1, ResultReader::read($statement, ResultKind::Key, countsBySelect: false));
    }

    #[Test]
    public function a_count_is_the_drivers_unless_the_dialect_reports_it_and_a_none_reads_nothing(): void
    {
        $this->pdo->exec('insert into t (n) values (1), (2), (3)');
        $statement = $this->pdo->prepare('update t set n = n + 1 where n > 1');
        $statement->execute();

        self::assertSame(2, ResultReader::read($statement, ResultKind::Count, countsBySelect: false));
        self::assertSame(2, ResultReader::read($statement, ResultKind::CommandCount, countsBySelect: false));
        self::assertNull(ResultReader::read($statement, ResultKind::None, countsBySelect: false));
    }

    #[Test]
    public function a_reported_count_is_an_integer_whatever_the_drivers_spelling(): void
    {
        $statement = $this->pdo->prepare("select '3'");   // pdo_odbc hands @@rowcount back as a string
        $statement->execute();

        self::assertSame(3, ResultReader::read($statement, ResultKind::Count, countsBySelect: true));
        self::assertSame(3, ResultReader::reportedCount(3));
        self::assertSame(0, ResultReader::reportedCount('0'));
    }

    #[Test]
    public function an_absent_reported_count_is_a_dialect_bug_never_zero(): void
    {
        $this->expectException(LogicException::class);

        ResultReader::reportedCount(null);
    }

    #[Test]
    public function a_non_numeric_reported_count_is_refused_too(): void
    {
        $this->expectException(LogicException::class);

        ResultReader::reportedCount('three');
    }
}
