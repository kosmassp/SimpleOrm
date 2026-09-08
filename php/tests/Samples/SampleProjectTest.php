<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Samples;

use DateTimeImmutable;
use DateTimeZone;
use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Discovery\ClassScanner;
use SimpleOrm\Mapping\JsonTypeHandler;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Sample\Models\Tables\Role;
use SimpleOrm\Sample\Models\Tables\Transaction;
use SimpleOrm\Sample\Models\Tables\TransactionDetail;
use SimpleOrm\Sample\Models\Tables\TransactionStatus;
use SimpleOrm\Sample\Models\Tables\User;
use SimpleOrm\Sample\Models\Views\UserTransactionTotal;
use SimpleOrm\Sample\Registry\DetailLine;
use SimpleOrm\Sample\Registry\Queries;
use SimpleOrm\Sample\Registry\TransactionsByUserArgs;
use SimpleOrm\Sample\Repositories\TransactionRepository;
use SimpleOrm\Sample\Repositories\UserRepository;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;
use SimpleOrm\Validation\SchemaGuard;

/**
 * The sample project (`samples/SimpleOrm.Sample`) as an integration fixture,
 * the way `dotnet/tests` drive `SimpleOrm.Sample`: its migration history
 * applies, validates under SchemaGuard, rolls back entirely through derived
 * downs (ADR-0018, no `down()` anywhere in the sample), and its repositories
 * and registry work against the migrated schema.
 */
final class SampleProjectTest extends TestCase
{
    private const string NAMESPACE = 'SimpleOrm\Sample';

    private TempDatabase $fixture;

    private Db $db;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $handlers = new TypeHandlerRegistry();
        $handlers->register(new JsonTypeHandler(DetailLine::class, list: true));
        $this->db = Db::open($this->fixture->connectionString(), new DbOptions(new SqliteDialect(), typeHandlers: $handlers));
    }

    protected function tearDown(): void
    {
        $this->db->close();
        $this->fixture->delete();
    }

    #[Test]
    public function the_whole_sample_history_applies_validates_and_rolls_back_without_any_down_code(): void
    {
        $runner = $this->runner();

        self::assertSame(9, $runner->migrate());
        self::assertSame(0, $runner->migrate());
        SchemaGuard::validate($this->db, self::classes(), $this->set(), $this->snapshots());

        self::assertSame(9, $runner->migrateDown(0));
        self::assertSame([], $this->userObjects());

        self::assertSame(9, $runner->migrate());
        $names = array_map(static fn (Role $role): string => $role->name, $this->db->queryAll(Role::class));
        self::assertSame(['admin', 'user'], $names);
    }

    #[Test]
    public function partial_rollback_reverses_the_rename_and_keeps_data(): void
    {
        $runner = $this->runner();
        $runner->migrate();

        self::assertSame(6, $runner->migrateDown(3));

        $statement = $this->db->connection()->query('select name from roles order by name');
        self::assertNotFalse($statement);
        self::assertContains('admin', $statement->fetchAll(PDO::FETCH_COLUMN));
    }

    #[Test]
    public function repositories_and_registry_work_against_the_migrated_schema(): void
    {
        $this->runner()->migrate();
        $users = new UserRepository($this->db);
        $transactions = new TransactionRepository($this->db);
        $now = new DateTimeImmutable('2026-09-08T12:00:00', new DateTimeZone('UTC'));

        $ada = new User();
        $ada->name = 'Ada';
        $ada->email = 'ada@example.com';
        $ada->createdAtUtc = $now;
        $users->insert($ada);

        $transaction = new Transaction();
        $transaction->userId = $ada->id;
        $transaction->amount = Decimal::of('15.25');
        $transaction->createdAtUtc = $now;
        $transactions->insert($transaction);

        $detail = new TransactionDetail();
        $detail->transactionId = $transaction->id;
        $detail->description = 'cake';
        $detail->quantity = 2;
        $detail->unitPrice = Decimal::of('5.00');
        $detail->createdAtUtc = $now;
        $this->db->insert($detail);

        self::assertSame($ada->id, $users->getByEmail('ada@example.com')->id);
        self::assertCount(1, $transactions->getByStatus(TransactionStatus::Pending));

        $transactions->setStatus($transaction->id, TransactionStatus::Completed, $now);
        self::assertSame(1, $transactions->get($transaction->id)->version);

        $nested = $this->db->query(Queries::transactionsWithDetails(), new TransactionsByUserArgs($ada->id));
        self::assertCount(1, $nested);
        self::assertSame('cake', $nested[0]->details[0]->description);

        $totals = $this->db->get(UserTransactionTotal::class, $ada->id);
        self::assertSame(1, $totals->transactionCount);
        self::assertTrue($totals->totalAmount->equals(Decimal::of('15.25')));

        $daily = $transactions->getDailySales($now->modify('-1 day'));
        self::assertCount(1, $daily);
        self::assertSame('2026-09-08', $daily[0]->salesDate->format('Y-m-d'));
    }

    private function runner(): MigrationRunner
    {
        return new MigrationRunner(
            $this->db->connection(),
            $this->db->options()->dialect,
            $this->db->maps(),
            $this->set(),
            $this->snapshots(),
        );
    }

    private function set(): MigrationSet
    {
        return MigrationSet::fromDirectory(self::migrationsDir(), self::NAMESPACE . '\Migrations');
    }

    private function snapshots(): SnapshotSet
    {
        return SnapshotSet::fromDirectory(self::migrationsDir());
    }

    /** @return list<class-string> */
    private static function classes(): array
    {
        return ClassScanner::classes(self::sourceDir(), self::NAMESPACE);
    }

    private static function sourceDir(): string
    {
        return dirname(__DIR__, 2) . '/samples/SimpleOrm.Sample/src';
    }

    private static function migrationsDir(): string
    {
        return self::sourceDir() . '/Migrations';
    }

    /** @return list<string> every table and view except the runner's own version table */
    private function userObjects(): array
    {
        $statement = $this->db->connection()->query(
            "select name from sqlite_master where type in ('table', 'view') and name not like 'sqlite_%' and name <> 'schema_version' order by name",
        );
        self::assertNotFalse($statement);

        return $statement->fetchAll(PDO::FETCH_COLUMN);
    }
}
