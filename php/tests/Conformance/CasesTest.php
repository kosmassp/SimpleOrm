<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use DateTimeImmutable;
use DateTimeZone;
use PDO;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;
use UnitEnum;

/**
 * The conformance-case runner (§9, spec/mapping-rules.md): each
 * `conformance/cases/*.json` runs against a fresh database built from entity
 * metadata (never hand-written DDL, never the migrations tree — a separate,
 * in-flux area of this port) and seeded from `conformance/fixtures/seed.json`;
 * results compare via the documented value encoding, errors by code. Mirrors
 * dotnet's `ConformanceCaseTests` + `ConformanceDatabase`.
 */
final class CasesTest extends TestCase
{
    /** @var array<string, class-string> */
    private const ENTITY_TYPES = [
        'User' => User::class,
        'Role' => Role::class,
        'UserRole' => UserRole::class,
        'UserProfile' => UserProfile::class,
        'Transaction' => Transaction::class,
        'TransactionDetail' => TransactionDetail::class,
        'UserTransactionTotal' => UserTransactionTotal::class,
    ];

    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('cases') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        $path = ConformancePaths::dir('cases') . DIRECTORY_SEPARATOR . $fileName;
        $spec = json_decode((string) file_get_contents($path), associative: true, flags: JSON_THROW_ON_ERROR);

        $fixture = TempDatabase::create();
        try {
            self::buildFixture($fixture);

            if (isset($spec['expect']['error'])) {
                $actual = self::runExpectingError($fixture, $spec['result'], $spec['query']);
                self::assertSame($spec['expect']['error'], $actual, $fileName);

                return;
            }

            $actual = $spec['result'] === 'raw'
                ? self::runRaw($fixture, $spec['query'])
                : self::runEntity($fixture, $spec['result'], $spec['query']);
            self::assertEquals($spec['expect']['rows'], $actual, $fileName);
        } finally {
            $fixture->delete();
        }
    }

    private static function buildFixture(TempDatabase $fixture): void
    {
        $db = Db::open($fixture->connectionString(), new DbOptions(new SqliteDialect()));
        $db->createTable(User::class);
        $db->createTable(Role::class);
        $db->createTable(UserRole::class);
        $db->createTable(UserProfile::class);
        $db->createTable(Transaction::class);
        $db->createTable(TransactionDetail::class);
        $db->createView(UserTransactionTotal::class);
        $db->close();

        $seed = json_decode(
            (string) file_get_contents(ConformancePaths::dir('fixtures') . DIRECTORY_SEPARATOR . 'seed.json'),
            associative: true,
            flags: JSON_THROW_ON_ERROR,
        );

        $pdo = (new SqliteDialect())->createConnection($fixture->connectionString());
        foreach ($seed as $table => $rows) {
            foreach ($rows as $row) {
                $columns = array_keys($row);
                // Column names come from the fixture's own keys (trusted test
                // data, never user input); values always bind as parameters.
                $sql = 'insert into ' . $table . ' (' . implode(', ', $columns) . ') values ('
                    . implode(', ', array_map(static fn (string $c): string => ':' . $c, $columns)) . ')';
                PdoBinder::bindAndExecute($pdo->prepare($sql), $row);
            }
        }
    }

    private static function runRaw(TempDatabase $fixture, string $query): array
    {
        $pdo = (new SqliteDialect())->createConnection($fixture->connectionString());
        $statement = $pdo->prepare($query);
        $statement->execute();

        $rows = [];
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            $rows[] = $row;
        }

        return $rows;
    }

    private static function runEntity(TempDatabase $fixture, string $entityName, string $query): array
    {
        [$db, $rows] = self::executeEntityQuery($fixture, $entityName, $query);
        $map = $db->maps()->load(self::ENTITY_TYPES[$entityName]);
        try {
            return array_map(static fn (object $entity): array => self::encodeEntity($map, $entity), $rows);
        } finally {
            $db->close();
        }
    }

    private static function runExpectingError(TempDatabase $fixture, string $entityName, string $query): string
    {
        try {
            self::executeEntityQuery($fixture, $entityName, $query);

            return '(no error)';
        } catch (SimpleOrmException $exception) {
            return $exception->errorCode;
        }
    }

    /** @return array{0: Db, 1: list<object>} */
    private static function executeEntityQuery(TempDatabase $fixture, string $entityName, string $query): array
    {
        $entityType = self::ENTITY_TYPES[$entityName];
        $db = Db::open($fixture->connectionString(), new DbOptions(new SqliteDialect()));
        $registeredQuery = Query::inline(EmptyArgs::class, $entityType, $query);
        $rows = $db->query($registeredQuery, EmptyArgs::value());

        return [$db, $rows];
    }

    /** The documented conformance value encoding (spec/mapping-rules.md). */
    private static function encodeEntity(EntityMap $map, object $entity): array
    {
        $row = [];
        foreach ($map->properties as $property) {
            $row[$property->columnName] = self::encodeValue($property->getValue($entity));
        }

        return $row;
    }

    private static function encodeValue(mixed $value): mixed
    {
        return match (true) {
            $value === null, is_bool($value), is_int($value), is_float($value), is_string($value) => $value,
            $value instanceof Decimal => (string) $value,
            $value instanceof DateTimeImmutable => $value->setTimezone(new DateTimeZone('UTC'))->format('Y-m-d\TH:i:s.u') . '0Z',
            $value instanceof UnitEnum => $value->name,
            default => (string) $value,
        };
    }
}
