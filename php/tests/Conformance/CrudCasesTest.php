<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use DateTimeImmutable;
use DateTimeZone;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;
use UnitEnum;

/**
 * The crud-case runner (§9, spec/crud.md): generated insert/get/update/delete
 * plus optimistic concurrency, replayed as ordered step scripts against a
 * fresh database built from entity metadata. `"as"`/`"from"` snapshot and
 * replay entity instances so stale-version conflicts (`CRUD-010`) are
 * expressible as data; `"$last"` is the most recent insert's key. Mirrors
 * dotnet's `ConformanceCrudTests`.
 */
final class CrudCasesTest extends TestCase
{
    /** @var array<string, class-string> */
    private const ENTITY_TYPES = [
        'User' => User::class,
        'Transaction' => Transaction::class,
    ];

    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('crud-cases') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        $path = ConformancePaths::dir('crud-cases') . DIRECTORY_SEPARATOR . $fileName;
        $spec = json_decode((string) file_get_contents($path), associative: true, flags: JSON_THROW_ON_ERROR);

        $fixture = TempDatabase::create();
        try {
            $db = Db::open($fixture->connectionString(), new DbOptions(new SqliteDialect()));
            $db->createTable(User::class);
            $db->createTable(Transaction::class);

            $lastKey = null;
            $snapshots = [];

            foreach ($spec['steps'] as $step) {
                $expectedError = $step['expect']['error'] ?? null;
                $actualError = null;
                $result = null;

                try {
                    [$result, $lastKey] = self::runStep($db, $step, $lastKey, $snapshots);
                } catch (SimpleOrmException $exception) {
                    $actualError = $exception->errorCode;
                }

                self::assertSame($expectedError, $actualError, "{$fileName}: " . json_encode($step));

                if ($result !== null && $expectedError === null && isset($step['expect']['values'])) {
                    self::assertValues($db, $result, $step['expect']['values'], $fileName);
                }
            }
        } finally {
            $fixture->delete();
        }
    }

    /**
     * @param array<string, object> $snapshots appended to, by reference, when the step carries "as"
     * @return array{0: ?object, 1: mixed} the step's "get" result (for value assertions) and the updated $last key
     */
    private static function runStep(Db $db, array $step, mixed $lastKey, array &$snapshots): array
    {
        $result = null;

        switch ($step['op']) {
            case 'insert':
                $type = self::ENTITY_TYPES[$step['entity']];
                $entity = new $type();
                self::applyValues($db, $entity, $step['values']);
                $db->insert($entity);
                $lastKey = $db->maps()->load($type)->getKeyValues($entity)[0];
                break;

            case 'get':
                $type = self::ENTITY_TYPES[$step['entity']];
                $result = $db->get($type, self::keyOf($step, $lastKey));
                if (isset($step['as'])) {
                    $snapshots[$step['as']] = $result;
                }

                break;

            case 'update':
                $entity = $snapshots[$step['from']];
                self::applyValues($db, $entity, $step['values']);
                if (array_key_exists('columns', $step)) {
                    // Update by column list (ADR-0028): columns resolve to property
                    // names; an unknown column passes through so CRUD-005 is reachable.
                    $map = $db->maps()->load($entity::class);
                    $properties = [];
                    foreach ($step['columns'] as $columnName) {
                        $properties[] = self::propertyNameByColumn($map, $columnName);
                    }

                    $db->updateOnly($entity, $properties);
                    break;
                }

                $db->update($entity);
                break;

            case 'delete':
                if (isset($step['from'])) {
                    $entity = $snapshots[$step['from']];
                    $db->delete($entity::class, $entity);
                } else {
                    $type = self::ENTITY_TYPES[$step['entity']];
                    $db->delete($type, self::keyOf($step, $lastKey));
                }

                break;

            default:
                throw new RuntimeException("unknown op '{$step['op']}'");
        }

        return [$result, $lastKey];
    }

    private static function keyOf(array $step, mixed $lastKey): mixed
    {
        $key = $step['key'];

        return $key === '$last' ? ($lastKey ?? throw new RuntimeException('no prior insert for $last')) : $key;
    }

    /** @param array<string, mixed> $values keyed by column name, conformance value encoding */
    private static function applyValues(Db $db, object $entity, array $values): void
    {
        $map = $db->maps()->load($entity::class);
        foreach ($values as $columnName => $value) {
            $property = self::propertyByColumn($map, $columnName);
            $property->setValue($entity, self::decodeValue($value, $property));
        }
    }

    /** @param array<string, mixed> $expected keyed by column name, conformance value encoding */
    private static function assertValues(Db $db, object $entity, array $expected, string $fileName): void
    {
        $map = $db->maps()->load($entity::class);
        foreach ($expected as $columnName => $expectedValue) {
            $property = self::propertyByColumn($map, $columnName);
            $actual = self::encodeForCompare($property->getValue($entity));
            self::assertSame(self::expectedText($expectedValue), $actual, "{$fileName}: column {$columnName}");
        }
    }

    private static function propertyNameByColumn(EntityMap $map, string $columnName): string
    {
        foreach ($map->properties as $property) {
            if ($property->columnName === $columnName) {
                return $property->propertyName();
            }
        }

        return $columnName;
    }

    private static function propertyByColumn(EntityMap $map, string $columnName): PropertyMap
    {
        foreach ($map->properties as $property) {
            if ($property->columnName === $columnName) {
                return $property;
            }
        }

        throw new RuntimeException("no mapped property for column '{$columnName}' on {$map->entityName()}");
    }

    /** The documented conformance value encoding, decoded into a PHP value for the property's type. */
    private static function decodeValue(mixed $value, PropertyMap $property): mixed
    {
        if ($value === null) {
            return null;
        }

        return match ($property->type) {
            ColumnType::Int16, ColumnType::Int32, ColumnType::Int64 => (int) $value,
            ColumnType::Decimal => Decimal::of((string) $value),
            ColumnType::Double, ColumnType::Float => (float) $value,
            ColumnType::Bool => (bool) $value,
            ColumnType::EnumText, ColumnType::EnumInt => self::enumCase($property->phpType, (string) $value),
            ColumnType::DateTime, ColumnType::DateTimeOffset, ColumnType::Date, ColumnType::Time
                => new DateTimeImmutable((string) $value),
            default => (string) $value,
        };
    }

    /** @param class-string|null $enumClass */
    private static function enumCase(?string $enumClass, string $value): UnitEnum
    {
        if ($enumClass === null || !enum_exists($enumClass)) {
            throw new RuntimeException("'{$enumClass}' is not an enum");
        }

        foreach ($enumClass::cases() as $case) {
            if (strcasecmp($case->name, $value) === 0) {
                return $case;
            }
        }

        throw new RuntimeException("'{$value}' is not a case of {$enumClass}");
    }

    /** The documented conformance value encoding, reduced to a comparable string (or null). */
    private static function encodeForCompare(mixed $value): ?string
    {
        return match (true) {
            $value === null => null,
            is_bool($value) => $value ? 'true' : 'false',
            is_int($value) => (string) $value,
            is_float($value) => (string) $value,
            $value instanceof Decimal => (string) $value,
            $value instanceof DateTimeImmutable => $value->setTimezone(new DateTimeZone('UTC'))->format('Y-m-d\TH:i:s.u') . '0Z',
            $value instanceof UnitEnum => $value->name,
            default => (string) $value,
        };
    }

    private static function expectedText(mixed $jsonValue): ?string
    {
        return match (true) {
            $jsonValue === null => null,
            is_bool($jsonValue) => $jsonValue ? 'true' : 'false',
            default => (string) $jsonValue,
        };
    }
}
