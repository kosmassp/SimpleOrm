<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\ConformanceSeed;
use SimpleOrm\Tests\Support\ConformanceValues;
use SimpleOrm\Tests\Support\RecordingUpdateDialect;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * The tracking-case runner (Level 3 M1, ADR-0034 add.1, spec/session.md "Conformance:
 * tracking-cases/"): each `conformance/tracking-cases/*.json` replays a step script against named
 * instances on the seeded fixture database, in the case's tracking mode, through a
 * {@see RecordingUpdateDialect} so `expectWritten` is observable at the dialect seam.
 *
 * Property names and navigations are spelled as the reference declares them (`Name`,
 * `Address.City`, `User`, `Transactions`); this runner maps them to the PHP declaration with
 * `lcfirst` per dotted segment (`name`, `address.city`), as {@see LoadCasesTest} does for
 * navigations — the library calls themselves stay exact. `dirty.properties` and
 * `expectWritten` lists compare as sets: the spec fixes no order for them.
 */
final class TrackingCasesTest extends TestCase
{
    /** @var array<string, class-string> */
    private const array ENTITY_TYPES = [
        'User' => User::class,
        'Role' => Role::class,
        'UserRole' => UserRole::class,
        'UserProfile' => UserProfile::class,
        'Transaction' => Transaction::class,
        'TransactionDetail' => TransactionDetail::class,
    ];

    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('tracking-cases') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        $spec = json_decode(
            (string) file_get_contents(ConformancePaths::dir('tracking-cases') . DIRECTORY_SEPARATOR . $fileName),
            associative: true,
            flags: JSON_THROW_ON_ERROR,
        );

        $recorder = new RecordingUpdateDialect(new SqliteDialect());
        $mode = match ($spec['tracking']) {
            'tracked' => TrackingMode::Tracked,
            'untracked' => TrackingMode::Untracked,
            default => throw new RuntimeException("{$fileName}: unknown tracking mode '{$spec['tracking']}'"),
        };

        $fixture = TempDatabase::create();
        $db = SampleDatabase::open($fixture, new DbOptions($recorder, tracking: $mode));
        ConformanceSeed::insert($fixture);

        try {
            /** @var array<string, object> $refs */
            $refs = [];
            foreach ($spec['steps'] as $index => $step) {
                $context = "{$fileName} step {$index}: " . json_encode($step);
                $expectedError = $step['expectError'] ?? null;
                $actualError = null;

                try {
                    $this->runStep($db, $recorder, $step, $refs, $context);
                } catch (SimpleOrmException $exception) {
                    $actualError = $exception->errorCode;
                }

                self::assertSame($expectedError, $actualError, $context);
            }
        } finally {
            $db->close();
            $fixture->delete();
        }
    }

    /** @param array<string, object> $refs named instances, appended to by `get`/`new` */
    private function runStep(Db $db, RecordingUpdateDialect $recorder, array $step, array &$refs, string $context): void
    {
        if (isset($step['get'])) {
            $get = $step['get'];
            $type = self::entityType($get['entity']);
            $key = isset($get['keyOf'])
                ? $db->maps()->load($type)->getKeyValues(self::resolve($refs, $get['keyOf']))
                : $get['key'];
            $refs[$get['as']] = $db->get($type, $key);
        } elseif (isset($step['new'])) {
            $refs[$step['new']['as']] = $this->construct($db, $step['new']);
        } elseif (isset($step['set'])) {
            $set = $step['set'];
            $entity = self::resolve($refs, $set['ref']);
            $property = self::property($db->maps()->load($entity::class), $set['property']);
            $property->setValue($entity, ConformanceValues::decode($set['value'], $property));
        } elseif (isset($step['insert'])) {
            $db->insert(self::resolve($refs, $step['insert']['ref']));
        } elseif (isset($step['update'])) {
            $recorder->updates = [];
            $db->update(self::resolve($refs, $step['update']['ref']));
            if (array_key_exists('expectWritten', $step['update'])) {
                self::assertWritten($recorder->updates, $step['update']['expectWritten'], $context);
            }
        } elseif (isset($step['updateOnly'])) {
            $entity = self::resolve($refs, $step['updateOnly']['ref']);
            $db->updateOnly($entity, array_map(self::propertyName(...), $step['updateOnly']['properties']));
        } elseif (isset($step['delete'])) {
            $delete = $step['delete'];
            if (isset($delete['ref'])) {
                $entity = self::resolve($refs, $delete['ref']);
                $db->delete($entity::class, $entity);
            } else {
                $db->delete(self::entityType($delete['entity']), $delete['key']);
            }
        } elseif (isset($step['refresh'])) {
            $db->refresh(self::resolve($refs, $step['refresh']['ref']));
        } elseif (isset($step['load'])) {
            $db->load(self::resolve($refs, $step['load']['ref']), lcfirst($step['load']['navigation']));
        } elseif (isset($step['expect'])) {
            $this->expect($db, $step['expect'], $refs, $context);
        } else {
            throw new RuntimeException("unknown step: {$context}");
        }
    }

    /** @param array<string, object> $refs */
    private function expect(Db $db, array $expect, array $refs, string $context): void
    {
        if (isset($expect['same'])) {
            self::assertSame(self::resolve($refs, $expect['same'][0]), self::resolve($refs, $expect['same'][1]), $context);
        } elseif (isset($expect['notSame'])) {
            self::assertNotSame(self::resolve($refs, $expect['notSame'][0]), self::resolve($refs, $expect['notSame'][1]), $context);
        } elseif (isset($expect['dirty'])) {
            // spec/session.md Clarifications (ADR-0035): the runner may compare the lists as sets (the metadata order is pinned in TrackingTest).
            $actual = $db->changesOf(self::resolve($refs, $expect['dirty']['ref']));
            $expected = array_map(self::propertyName(...), $expect['dirty']['properties']);
            sort($actual);
            sort($expected);
            self::assertSame($expected, $actual, $context);
        } elseif (isset($expect['value'])) {
            $entity = self::resolve($refs, $expect['value']['ref']);
            $property = self::property($db->maps()->load($entity::class), $expect['value']['property']);
            self::assertSame(
                ConformanceValues::expectedText($expect['value']['equals']),
                ConformanceValues::encodeForCompare($property->getValue($entity)),
                $context,
            );
        } elseif (isset($expect['tracked'])) {
            self::assertSame($expect['tracked']['is'], $db->isTracked(self::resolve($refs, $expect['tracked']['ref'])), $context);
        } else {
            throw new RuntimeException("unknown expectation: {$context}");
        }
    }

    /**
     * `expectWritten`: `[]` — no update statement rendered; `"all"` — one full-row update; a list —
     * one column-list update over exactly those properties.
     *
     * @param list<'all'|list<string>> $rendered what the dialect was asked to render
     */
    private static function assertWritten(array $rendered, mixed $expected, string $context): void
    {
        if ($expected === 'all') {
            self::assertSame(['all'], $rendered, $context);

            return;
        }

        if ($expected === []) {
            self::assertSame([], $rendered, $context);

            return;
        }

        self::assertCount(1, $rendered, $context);
        $properties = $rendered[0];
        self::assertIsArray($properties, $context);
        $wanted = array_map(self::propertyName(...), $expected);
        sort($properties);
        sort($wanted);
        self::assertSame($wanted, $properties, $context);
    }

    /** @param array{entity: string, as: string, values: array<string, mixed>} $new */
    private function construct(Db $db, array $new): object
    {
        $type = self::entityType($new['entity']);
        $map = $db->maps()->load($type);
        $entity = new $type();
        foreach ($new['values'] as $name => $value) {
            $property = self::property($map, $name);
            $property->setValue($entity, ConformanceValues::decode($value, $property));
        }

        return $entity;
    }

    /**
     * A ref, one navigation hop (`t.User`), or an indexed element of a loaded collection
     * (`a.Transactions[0]`), the navigation spelled as the reference declares it.
     *
     * @param array<string, object> $refs
     */
    private static function resolve(array $refs, string $reference): object
    {
        if (preg_match('/^(\w+)(?:\.(\w+))?(?:\[(\d+)\])?$/', $reference, $m) !== 1) {
            throw new RuntimeException("malformed ref '{$reference}'");
        }

        $entity = $refs[$m[1]] ?? throw new RuntimeException("no ref named '{$m[1]}'");
        if (isset($m[2]) && $m[2] !== '') {
            $navigation = lcfirst($m[2]);
            $entity = $entity->{$navigation};
            if (isset($m[3]) && $m[3] !== '') {
                $entity = $entity[(int) $m[3]];
            }
        }

        return $entity ?? throw new RuntimeException("ref '{$reference}' resolves to null");
    }

    /** The reference's property spelling (`Address.City`) in this port's declaration (`address.city`). */
    private static function propertyName(string $name): string
    {
        return implode('.', array_map(lcfirst(...), explode('.', $name)));
    }

    private static function property(EntityMap $map, string $name): PropertyMap
    {
        return $map->property(self::propertyName($name))
            ?? throw new RuntimeException("no mapped property '{$name}' on {$map->entityName()}");
    }

    /** @return class-string */
    private static function entityType(string $name): string
    {
        return self::ENTITY_TYPES[$name] ?? throw new RuntimeException("unknown entity '{$name}'");
    }
}
