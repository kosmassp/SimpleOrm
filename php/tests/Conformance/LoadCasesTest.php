<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use DateTimeImmutable;
use DateTimeZone;
use LogicException;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\FetchMode;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\ConformanceSeed;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;
use UnitEnum;

/**
 * The conformance-case runner for relationship loading (§9, spec/loading.md):
 * each `conformance/load-cases/*.json` replays against a fresh database built
 * from entity metadata and seeded from `conformance/fixtures/seed.json`
 * (mirroring `Conformance\CasesTest`'s fixture, factored into
 * {@see ConformanceSeed} so this runner does not duplicate it). Every case
 * runs `explicit` (`Db::loadEach()` directly); a case marked `"viaQuery":
 * true` additionally replays through `Db::from()->include()->fetch()` under
 * every {@see FetchMode} against the same `loaded` expectations
 * (spec/loading.md: "load cases marked viaQuery: true replay through Include
 * under all three modes").
 *
 * SPEC-GAP: `load.navigation` names the C#-style declaration (PascalCase,
 * e.g. "Transactions"); the PHP declaration is the camelCase property
 * (`transactions`). The spec says the name is exact "like the declaration",
 * which is a per-language notion — this runner translates with `lcfirst()`
 * (see the case loop below).
 */
final class LoadCasesTest extends TestCase
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

    /** @return array<string, array{0: string, 1: string}> */
    public static function caseModes(): array
    {
        $combinations = [];
        foreach (ConformancePaths::cases('load-cases') as $file) {
            $spec = self::readCase($file);
            $modes = ['explicit'];
            if ($spec['viaQuery'] ?? false) {
                $modes = [...$modes, 'MultiQuery', 'SubSelect', 'Join'];
            }

            foreach ($modes as $mode) {
                $combinations["{$file} [{$mode}]"] = [$file, $mode];
            }
        }

        return $combinations;
    }

    #[Test]
    #[DataProvider('caseModes')]
    public function load_case_matches_the_expectation(string $fileName, string $mode): void
    {
        $spec = self::readCase($fileName);
        $entityType = self::ENTITY_TYPES[$spec['load']['entity']];
        // SPEC-GAP (see class docblock): translate the spec's PascalCase
        // declaration name to this port's camelCase property.
        $navigation = lcfirst($spec['load']['navigation']);

        $fixture = TempDatabase::create();
        $db = SampleDatabase::open($fixture);
        ConformanceSeed::insert($fixture);

        try {
            if (isset($spec['expect']['error'])) {
                try {
                    self::runCase($db, $entityType, $navigation, $spec['load']['keys'], $mode);
                    self::fail("{$fileName} [{$mode}]: expected {$spec['expect']['error']}, no error was thrown");
                } catch (SimpleOrmException $e) {
                    self::assertSame($spec['expect']['error'], $e->errorCode, "{$fileName} [{$mode}]");
                }

                return;
            }

            $owners = self::runCase($db, $entityType, $navigation, $spec['load']['keys'], $mode);
            $map = $db->maps()->load($entityType);

            $actualByKey = [];
            foreach ($owners as $owner) {
                $actualByKey[self::keyString($map, $owner)] = $owner->{$navigation};
            }

            foreach ($spec['expect']['loaded'] as $keyString => $expected) {
                self::assertArrayHasKey($keyString, $actualByKey, "{$fileName} [{$mode}]: owner {$keyString} missing from the result");
                self::assertLoaded($db, $expected, $actualByKey[$keyString], "{$fileName} [{$mode}] owner {$keyString}");
            }
        } catch (LogicException $e) {
            // The join engine is agent (c)'s stub (DbEagerJoin) — skip only
            // its own not-implemented refusal, never a genuine LogicException.
            if ($mode === 'Join' && str_contains($e->getMessage(), 'ADR-0032 step 2c')) {
                self::markTestSkipped("{$fileName} [Join]: join mode pending (c) — {$e->getMessage()}");
            }

            throw $e;
        } finally {
            $db->close();
            $fixture->delete();
        }
    }

    /**
     * Runs one case under one mode: `explicit` fetches each owner by key and
     * calls `Db::loadEach()` directly; the fetch modes replay the same owner
     * selection through `Db::from()->include()->fetch()` (spec/loading.md: "a
     * viaQuery replay's root query selects exactly the listed owners").
     *
     * @param list<mixed> $keys
     * @return list<object>
     */
    private static function runCase(Db $db, string $entityType, string $navigation, array $keys, string $mode): array
    {
        if ($mode === 'explicit') {
            // SPEC-GAP: spec/loading.md's "Conformance cases" section defines
            // exactly how a `viaQuery` replay selects its owners, but is silent
            // on how the (always-run) `explicit` replay should obtain them from
            // `load.keys`. Reading a case's `load` object as "the batch form"'s
            // input, fetching each owner by key and calling `loadEach()` once
            // over the whole batch seems the natural, and most direct,
            // reading — never one `load()` per owner.
            $owners = array_map(static fn (mixed $key): object => $db->get($entityType, $key), $keys);
            $db->loadEach($owners, $navigation);

            return $owners;
        }

        $map = $db->maps()->load($entityType);
        $fetchMode = match ($mode) {
            'MultiQuery' => FetchMode::MultiQuery,
            'SubSelect' => FetchMode::SubSelect,
            'Join' => FetchMode::Join,
        };

        return $db->from($entityType)
            ->where(self::keySelectionPredicate($map, $keys))
            ->include($navigation)
            ->fetch($fetchMode)
            ->toList();
    }

    /**
     * "for a single-column key, Where(In(<key property>, keys)); for a
     * composite key, an Or of one And of equalities per owner, parts in key
     * order" (spec/loading.md).
     *
     * @param list<mixed> $keys
     */
    private static function keySelectionPredicate(EntityMap $map, array $keys): Criteria
    {
        $keyProperties = array_map(static fn (PropertyMap $p): string => $p->propertyName(), $map->keyProperties);
        if (count($keyProperties) === 1) {
            return Criteria::in($keyProperties[0], $keys);
        }

        $ors = [];
        foreach ($keys as $tuple) {
            $ands = [];
            foreach ($keyProperties as $i => $name) {
                $ands[] = Criteria::eq($name, $tuple[$i]);
            }

            $ors[] = Criteria::and(...$ands);
        }

        return Criteria::or(...$ors);
    }

    /** Composite keys join their parts with `|` (spec/loading.md "Conformance cases"). */
    private static function keyString(EntityMap $map, object $entity): string
    {
        return implode('|', array_map(static fn (mixed $v): string => (string) $v, $map->getKeyValues($entity)));
    }

    /** `null` for a dead/absent singular navigation; a JSON array is an ordered collection; otherwise a singular row. */
    private static function assertLoaded(Db $db, mixed $expected, mixed $actual, string $context): void
    {
        if ($expected === null) {
            self::assertNull($actual, $context);

            return;
        }

        if (array_is_list($expected)) {
            self::assertIsArray($actual, $context);
            self::assertCount(count($expected), $actual, "{$context}: collection length");
            foreach ($expected as $i => $expectedRow) {
                self::assertRowMatches($db, $expectedRow, $actual[$i], "{$context}[{$i}]");
            }

            return;
        }

        self::assertIsObject($actual, $context);
        self::assertRowMatches($db, $expected, $actual, $context);
    }

    /** Listed columns are checked, others ignored (spec/loading.md "Conformance cases"). */
    private static function assertRowMatches(Db $db, array $expectedRow, object $actual, string $context): void
    {
        $map = $db->maps()->load($actual::class);
        foreach ($expectedRow as $column => $expectedValue) {
            $property = null;
            foreach ($map->properties as $candidate) {
                if ($candidate->columnName === $column) {
                    $property = $candidate;
                    break;
                }
            }

            self::assertNotNull($property, "{$context}: no mapped property for column '{$column}' on {$map->entityName()}");
            self::assertSame($expectedValue, self::encodeValue($property->getValue($actual)), "{$context} column {$column}");
        }
    }

    /** The documented conformance value encoding (spec/mapping-rules.md), matching `Conformance\CasesTest`. */
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

    /** @return array<string, mixed> */
    private static function readCase(string $fileName): array
    {
        return json_decode(
            (string) file_get_contents(ConformancePaths::dir('load-cases') . DIRECTORY_SEPARATOR . $fileName),
            associative: true,
            flags: JSON_THROW_ON_ERROR,
        );
    }
}
