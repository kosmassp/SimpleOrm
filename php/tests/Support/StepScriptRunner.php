<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PHPUnit\Framework\Assert;
use RuntimeException;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\OwnedMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\DbTransactionScope;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;

/**
 * The ONE step-script engine behind `Conformance\TrackingCasesTest`, `FlushCasesTest`, `HookCasesTest`, and
 * `MergeCasesTest` (CODING-STANDARD §8; spec/session.md "Conformance: tracking-cases/", spec/unit-of-work.md
 * "Conformance: flush-cases/" and "Detach and merge", spec/hooks.md "Conformance: hook-cases/"); the session clock
 * is fixed at {@see ConformanceClock::INSTANT}: a case file runs against the seeded fixture database in the
 * declared tracking mode, through a {@see RecordingUpdateDialect} so what the session asked the
 * dialect to render — an update's columns (`expectWritten`), a flush's ordered statements
 * (`expectStatements`) — is observable at the one seam every implementation renders SQL through.
 *
 * Property names and navigations are spelled as the reference declares them (`Name`,
 * `Address.City`, `User`, `Details`); the engine maps them to the PHP declaration with `lcfirst`
 * per dotted segment (`name`, `address.city`) — the library calls themselves stay exact.
 * Property lists (`dirty.properties`, `expectWritten`, an update's `columns`) compare as sets: the
 * spec fixes no order for them. A `count` expectation reads the relation through a **separate**
 * connection to the same file, so a rolled-back flush is visible.
 */
final class StepScriptRunner
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

    /** @var array<string, object> named instances, appended to by `get`/`new` */
    private array $refs = [];

    private ?DbTransactionScope $scope = null;

    private function __construct(
        private readonly Db $db,
        private readonly RecordingUpdateDialect $recorder,
        private readonly TempDatabase $fixture,
    ) {
    }

    /** Replays `conformance/<folder>/<fileName>` and asserts every expectation and `expectError` of its steps. */
    public static function replay(string $folder, string $fileName): void
    {
        $spec = json_decode(
            (string) file_get_contents(ConformancePaths::dir($folder) . DIRECTORY_SEPARATOR . $fileName),
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
        $db = SampleDatabase::open($fixture, new DbOptions($recorder, tracking: $mode, clock: ConformanceClock::fixed()));
        ConformanceSeed::insert($fixture);
        $runner = new self($db, $recorder, $fixture);

        try {
            foreach ($spec['steps'] as $index => $step) {
                $context = "{$fileName} step {$index}: " . json_encode($step);
                $expectedError = $step['expectError'] ?? null;
                $actualError = null;

                try {
                    $runner->runStep($step, $context);
                } catch (SimpleOrmException $exception) {
                    $actualError = $exception->errorCode;
                }

                Assert::assertSame($expectedError, $actualError, $context);
            }
        } finally {
            $runner->scope = null;
            $db->close();
            $fixture->delete();
        }
    }

    private function runStep(array $step, string $context): void
    {
        if (isset($step['get'])) {
            $get = $step['get'];
            $type = self::entityType($get['entity']);
            $key = isset($get['keyOf'])
                ? $this->db->maps()->load($type)->getKeyValues($this->resolve($get['keyOf']))
                : $get['key'];
            $this->refs[$get['as']] = $this->db->get($type, $key);
        } elseif (isset($step['new'])) {
            $this->refs[$step['new']['as']] = $this->construct($step['new']);
        } elseif (isset($step['set'])) {
            $set = $step['set'];
            $entity = $this->resolve($set['ref']);
            $property = self::property($this->db->maps()->load($entity::class), $set['property']);
            $property->setValue($entity, ConformanceValues::decode($set['value'], $property));
        } elseif (isset($step['insert'])) {
            $this->db->insert($this->resolve($step['insert']['ref']));
        } elseif (isset($step['update'])) {
            $this->recorder->updates = [];
            $this->db->update($this->resolve($step['update']['ref']));
            if (array_key_exists('expectWritten', $step['update'])) {
                self::assertWritten($this->recorder->updates, $step['update']['expectWritten'], $context);
            }
        } elseif (isset($step['updateOnly'])) {
            $entity = $this->resolve($step['updateOnly']['ref']);
            $this->db->updateOnly($entity, array_map(self::propertyName(...), $step['updateOnly']['properties']));
        } elseif (isset($step['delete'])) {
            $delete = $step['delete'];
            if (isset($delete['ref'])) {
                $entity = $this->resolve($delete['ref']);
                $this->db->delete($entity::class, $entity);
            } else {
                $this->db->delete(self::entityType($delete['entity']), $delete['key']);
            }
        } elseif (isset($step['detach'])) {
            $this->db->detach($this->resolve($step['detach']['ref']));
        } elseif (isset($step['merge'])) {
            $this->refs[$step['merge']['as']] = $this->db->merge($this->resolve($step['merge']['ref']));
        } elseif (isset($step['refresh'])) {
            $this->db->refresh($this->resolve($step['refresh']['ref']));
        } elseif (isset($step['load'])) {
            $this->db->load($this->resolve($step['load']['ref']), lcfirst($step['load']['navigation']));
        } elseif (isset($step['persist'])) {
            $this->db->persist($this->resolve($step['persist']['ref']));
        } elseif (isset($step['remove'])) {
            $this->db->remove($this->resolve($step['remove']['ref']));
        } elseif (isset($step['flush'])) {
            $this->flush($step['flush'], $context);
        } elseif (isset($step['begin'])) {
            $this->scope = $this->db->begin();
        } elseif (isset($step['commit'])) {
            $this->requireScope($context)->commit();
            $this->scope = null;
        } elseif (isset($step['rollback'])) {
            $this->requireScope($context)->rollback();
            $this->scope = null;
        } elseif (isset($step['expect'])) {
            $this->expect($step['expect'], $context);
        } else {
            throw new RuntimeException("unknown step: {$context}");
        }
    }

    /** `flush` with `expectStatements` (the ordered `{op, entity, columns?}` list the dialect was asked to render) and `expectSummary`. */
    private function flush(array $flush, string $context): void
    {
        $this->recorder->statements = [];
        $summary = $this->db->flush();

        if (array_key_exists('expectStatements', $flush)) {
            self::assertStatements($this->recorder->statements, $flush['expectStatements'], $context);
        }

        if (array_key_exists('expectSummary', $flush)) {
            Assert::assertSame(
                [
                    'inserted' => $flush['expectSummary']['inserted'],
                    'updated' => $flush['expectSummary']['updated'],
                    'deleted' => $flush['expectSummary']['deleted'],
                ],
                ['inserted' => $summary->inserted, 'updated' => $summary->updated, 'deleted' => $summary->deleted],
                $context,
            );
        }
    }

    private function requireScope(string $context): DbTransactionScope
    {
        return $this->scope ?? throw new RuntimeException("no open transaction: {$context}");
    }

    private function expect(array $expect, string $context): void
    {
        if (isset($expect['same'])) {
            Assert::assertSame($this->resolve($expect['same'][0]), $this->resolve($expect['same'][1]), $context);
        } elseif (isset($expect['notSame'])) {
            Assert::assertNotSame($this->resolve($expect['notSame'][0]), $this->resolve($expect['notSame'][1]), $context);
        } elseif (isset($expect['dirty'])) {
            // spec/session.md Clarifications (ADR-0035): the runner may compare the lists as sets (the metadata order is pinned in TrackingTest).
            $actual = $this->db->changesOf($this->resolve($expect['dirty']['ref']));
            $expected = array_map(self::propertyName(...), $expect['dirty']['properties']);
            sort($actual);
            sort($expected);
            Assert::assertSame($expected, $actual, $context);
        } elseif (isset($expect['value'])) {
            $entity = $this->resolve($expect['value']['ref']);
            $map = $this->db->maps()->load($entity::class);
            $name = $expect['value']['property'];
            $equals = $expect['value']['equals'];
            $owner = self::ownedNavigation($map, $name);
            if ($owner !== null) {
                // An owned navigation named whole: null, or {} for "an owned object exists" (spec/session.md "The step scripts").
                $owned = $owner->property->isInitialized($entity) ? $owner->property->getValue($entity) : null;
                Assert::assertSame($equals === null ? null : '{}', $owned === null ? null : '{}', $context);
            } else {
                $property = self::property($map, $name);
                Assert::assertSame(
                    ConformanceValues::expectedText($equals),
                    ConformanceValues::encodeForCompare($property->getValue($entity), $property),
                    $context,
                );
            }
        } elseif (isset($expect['tracked'])) {
            Assert::assertSame($expect['tracked']['is'], $this->db->isTracked($this->resolve($expect['tracked']['ref'])), $context);
        } elseif (isset($expect['pending'])) {
            Assert::assertSame($expect['pending'], $this->db->hasPendingWork(), $context);
        } elseif (isset($expect['sameValue'])) {
            Assert::assertSame(
                ConformanceValues::encodeForCompare($this->pathValue($expect['sameValue'][0])),
                ConformanceValues::encodeForCompare($this->pathValue($expect['sameValue'][1])),
                $context,
            );
        } elseif (isset($expect['count'])) {
            Assert::assertSame($expect['count']['equals'], $this->rowCount(self::entityType($expect['count']['entity'])), $context);
        } else {
            throw new RuntimeException("unknown expectation: {$context}");
        }
    }

    /** A `ref.Property` path read as the value of that (reference-spelled) property. */
    private function pathValue(string $path): mixed
    {
        [$ref, $name] = explode('.', $path, 2);
        $entity = $this->refs[$ref] ?? throw new RuntimeException("no ref named '{$ref}'");

        return self::property($this->db->maps()->load($entity::class), $name)->getValue($entity);
    }

    /** The relation's row count, read outside the session (a separate connection) so an uncommitted or rolled-back write is not seen. */
    private function rowCount(string $entityType): int
    {
        $map = $this->db->maps()->load($entityType);
        $dialect = new SqliteDialect();
        $connection = $dialect->createConnection($this->fixture->connectionString());
        // The identifier comes from the entity map and renders through the dialect; no user data is involved.
        $count = $connection->query('select count(*) from ' . $dialect->quoteIdentifier((string) $map->relationName))
            ?->fetchColumn(0);
        $connection = null;

        return (int) $count;
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
            Assert::assertSame(['all'], $rendered, $context);

            return;
        }

        if ($expected === []) {
            Assert::assertSame([], $rendered, $context);

            return;
        }

        Assert::assertCount(1, $rendered, $context);
        $properties = $rendered[0];
        Assert::assertIsArray($properties, $context);
        $wanted = array_map(self::propertyName(...), $expected);
        sort($properties);
        sort($wanted);
        Assert::assertSame($wanted, $properties, $context);
    }

    /**
     * The ordered statement list a flush must have rendered: each `{op, entity, columns?}`, `columns`
     * (updates only) the property list or `"all"`, compared as a set.
     *
     * @param list<array{op: string, entity: string, columns?: 'all'|list<string>}> $rendered
     * @param list<array{op: string, entity: string, columns?: 'all'|list<string>}> $expected
     */
    private static function assertStatements(array $rendered, array $expected, string $context): void
    {
        // An update's `columns` compare as a set, as `expectWritten` does (spec/unit-of-work.md Clarifications, ADR-0036);
        // `entity` is the short class name; `count` reads the relation through a second connection.
        $normalize = static function (array $statement): string {
            $columns = $statement['columns'] ?? null;
            if (is_array($columns)) {
                $columns = array_map(self::propertyName(...), $columns);
                sort($columns);
            }

            return json_encode([$statement['op'], $statement['entity'], $statement['op'] === 'update' ? $columns : null], JSON_THROW_ON_ERROR);
        };

        Assert::assertSame(array_map($normalize, $expected), array_map($normalize, $rendered), $context);
    }

    /** `new`: an instance from `values` (reference-spelled), with `navigations` — refs, or lists of refs — assigned to its `private(set)` navigations. */
    private function construct(array $new): object
    {
        $type = self::entityType($new['entity']);
        $map = $this->db->maps()->load($type);
        $entity = new $type();
        foreach ($new['values'] as $name => $value) {
            $property = self::property($map, $name);
            $property->setValue($entity, ConformanceValues::decode($value, $property));
        }

        foreach ($new['navigations'] ?? [] as $navigation => $target) {
            $relationship = null;
            foreach ($map->relationships as $candidate) {
                if ($candidate->propertyName === lcfirst($navigation)) {
                    $relationship = $candidate;
                }
            }

            $relationship ?? throw new RuntimeException("no navigation '{$navigation}' on {$map->entityName()}");
            $map->navigationProperty($relationship)->setValue(
                $entity,
                is_array($target) ? array_map($this->resolve(...), $target) : $this->resolve($target),
            );
        }

        return $entity;
    }

    /**
     * A ref, one navigation hop (`t.User`), or an indexed element of a loaded collection
     * (`a.Transactions[0]`), the navigation spelled as the reference declares it.
     */
    private function resolve(string $reference): object
    {
        if (preg_match('/^(\w+)(?:\.(\w+))?(?:\[(\d+)\])?$/', $reference, $m) !== 1) {
            throw new RuntimeException("malformed ref '{$reference}'");
        }

        $entity = $this->refs[$m[1]] ?? throw new RuntimeException("no ref named '{$m[1]}'");
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

    /** The owned navigation `$name` names whole (`Address`), if any. */
    private static function ownedNavigation(EntityMap $map, string $name): ?OwnedMap
    {
        foreach ($map->ownedTypes as $owner) {
            if ($owner->propertyName() === self::propertyName($name)) {
                return $owner;
            }
        }

        return null;
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
