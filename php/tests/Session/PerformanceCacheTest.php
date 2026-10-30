<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\PostgresDialect;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Dialect\SqlServerDialect;
use SimpleOrm\Mapping\ResultMapper;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\MappingOptions;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\GeneratedSql;
use SimpleOrm\Session\MapLabels;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\DelegatingDialect;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Tests\Support\WriteRenderingRecorder;
use SimpleOrm\Types\Decimal;

/**
 * The performance pass's caches (ADR-0048) compute once and change nothing observable: the generated CRUD text is
 * rendered once per (map, dialect) while the render hook still reports every rendering the session takes.
 */
final class PerformanceCacheTest extends TestCase
{
    private TempDatabase $fixture;

    private CountingWriteDialect $dialect;

    private Db $db;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->dialect = new CountingWriteDialect(new SqliteDialect());
        $this->db = SampleDatabase::open($this->fixture, new DbOptions($this->dialect));
    }

    protected function tearDown(): void
    {
        $this->db->close();
        $this->fixture->delete();
    }

    #[Test]
    public function the_generated_crud_text_is_rendered_once_per_map_and_every_rendering_is_still_reported(): void
    {
        $recorder = WriteRenderingRecorder::attach($this->db);

        $users = [];
        for ($i = 0; $i < 3; $i++) {
            $users[] = SampleDatabase::insertUser($this->db, "u{$i}", "u{$i}@example.com");
        }

        foreach ($users as $user) {
            $user->displayName = 'shown';
            $this->db->update($user);
            $this->db->updateOnly($user, ['displayName']);
            $this->db->updateOnly($user, ['name', 'displayName']);
            $this->db->updateOnly($user, ['displayName', 'name']);
        }

        foreach ($users as $user) {
            $this->db->delete(User::class, $user->id);
        }

        self::assertSame(1, $this->dialect->calls['insert']);
        self::assertSame(1, $this->dialect->calls['update']);
        // One rendering per distinct ordered list: the order is part of the key, so two orders are two texts.
        self::assertSame(3, $this->dialect->calls['updateOnly']);
        self::assertSame(1, $this->dialect->calls['delete']);

        // The hook reports what a wrapping dialect saw before the cache: every rendering the session took, in order.
        $ops = array_map(static fn (array $s): string => $s['op'], $recorder->statements);
        self::assertSame(
            ['insert', 'insert', 'insert', ...array_fill(0, 12, 'update'), 'delete', 'delete', 'delete'],
            $ops,
        );
        self::assertSame(
            ['all', ['displayName', 'updatedAtUtc'], ['name', 'displayName', 'updatedAtUtc'], ['displayName', 'name', 'updatedAtUtc']],
            array_slice($recorder->updates, 0, 4),
        );
    }

    #[Test]
    public function the_cache_is_keyed_by_the_map_instance_and_the_dialect_instance(): void
    {
        $loader = new EntityMapLoader(new MappingOptions());
        $map = $loader->load(User::class);
        $other = (new EntityMapLoader(new MappingOptions()))->load(User::class);
        $first = new CountingWriteDialect(new SqliteDialect());
        $second = new CountingWriteDialect(new SqliteDialect());

        $text = GeneratedSql::for($map, $first)->insert($map, $first);
        self::assertSame($text, GeneratedSql::for($map, $first)->insert($map, $first));
        self::assertSame(1, $first->calls['insert']);

        // Another dialect instance on the same map, and an equal map that is another instance, each render their own.
        GeneratedSql::for($map, $second)->insert($map, $second);
        GeneratedSql::for($other, $first)->insert($other, $first);
        self::assertSame(1, $second->calls['insert']);
        self::assertSame(2, $first->calls['insert']);
        self::assertNotSame(GeneratedSql::for($map, $first), GeneratedSql::for($other, $first));
        self::assertInstanceOf(EntityMap::class, $other);
    }

    #[Test]
    public function the_labels_are_built_once_per_map_and_read_as_the_call_sites_built_them(): void
    {
        $map = (new EntityMapLoader(new MappingOptions()))->load(User::class);
        $labels = MapLabels::for($map);

        self::assertSame($labels, MapLabels::for($map));
        self::assertSame('User', $map->entityName());
        self::assertSame('User.email', $labels->property($map->property('email')));
        self::assertSame('User key[0]', $labels->key(0));
        self::assertSame('User.insert', $labels->name('insert'));
        self::assertSame('User get', $labels->plan('get'));
        self::assertSame('User criteria', $labels->criteria);
        self::assertSame('User criteria', Db::criteriaQueryName($map));
        self::assertSame('User criteria @c3', $labels->criteriaParameter(3));
    }

    /**
     * The sort-skip rule (ADR-0048 item 4): only integer and decimal keys stored as an integer or exact numeric type skip
     * the per-owner sort. SQLite stores a decimal as TEXT, so a decimal key there keeps the sort — pinned end to end by
     * LoadingTest's and EagerJoinTest's "2 before 10" cases; this pins the rule itself per dialect.
     */
    #[Test]
    public function the_per_owner_sort_is_skipped_only_where_the_key_order_is_value_order(): void
    {
        $loader = new EntityMapLoader(new MappingOptions());
        $intKey = $loader->load(User::class);
        $decimalKey = $loader->load(DecimalKeyed::class);
        $temporalKey = $loader->load(TemporalKeyed::class);
        $rule = static fn (EntityMap $map, Dialect $dialect): bool => GeneratedSql::for($map, $dialect)
            ->keyOrderIsValueOrder($map, $dialect);

        foreach ([new SqliteDialect(), new PostgresDialect(), new SqlServerDialect()] as $dialect) {
            self::assertTrue($rule($intKey, $dialect), "integer key on {$dialect->name()}");
            self::assertFalse($rule($temporalKey, $dialect), "temporal key on {$dialect->name()}");
        }

        self::assertFalse($rule($decimalKey, new SqliteDialect()), 'SQLite stores a decimal as TEXT: the sort stays');
        self::assertTrue($rule($decimalKey, new PostgresDialect()), 'numeric on PostgreSQL');
        self::assertTrue($rule($decimalKey, new SqlServerDialect()), 'decimal on SQL Server');
    }

    /** The plan cache's structural key (ADR-0048 item 6): one plan per (result type, column-name list), order included. */
    #[Test]
    public function a_mapping_plan_is_built_once_per_result_type_and_column_list(): void
    {
        $mapper = new ResultMapper(new EntityMapLoader(new MappingOptions()), new TypeConverter(new TypeHandlerRegistry()));
        $columns = ['id', 'name', 'email', 'display_name', 'created_at', 'updated_at'];

        $plan = $mapper->createPlan(User::class, $columns, 'q');
        self::assertSame($plan, $mapper->createPlan(User::class, $columns, 'q'));
        self::assertNotSame($plan, $mapper->createPlan(User::class, array_reverse($columns), 'q'));
        self::assertSame(
            $mapper->createPlan(User::class, array_reverse($columns), 'q'),
            $mapper->createPlan(User::class, array_reverse($columns), 'q'),
        );
    }

    #[Test]
    public function the_key_read_and_the_select_all_are_the_texts_they_were(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'ada', 'ada@example.com');
        $lines = [];
        $db = Db::open($this->fixture->connectionString(), new DbOptions(
            new SqliteDialect(),
            log: static function (string $line) use (&$lines): void {
                $lines[] = $line;
            },
        ));
        try {
            $db->get(User::class, $user->id);
            $db->get(User::class, $user->id);
            $db->queryAll(User::class);
        } finally {
            $db->close();
        }

        $select = 'select id, name, email, display_name, created_at, updated_at from users';
        self::assertSame(
            [
                "User.get: {$select} where id = @k0 [@k0]",
                "User.get: {$select} where id = @k0 [@k0]",
                "User.selectAll: {$select} order by id",
            ],
            $lines,
        );
    }
}

/** Counts the four write renderings the session asks the dialect for. */
final class CountingWriteDialect extends DelegatingDialect
{
    /** @var array{insert: int, update: int, updateOnly: int, delete: int} */
    public array $calls = ['insert' => 0, 'update' => 0, 'updateOnly' => 0, 'delete' => 0];

    public function insertSql(EntityMap $map): string
    {
        $this->calls['insert']++;

        return $this->inner->insertSql($map);
    }

    public function updateSql(EntityMap $map): string
    {
        $this->calls['update']++;

        return $this->inner->updateSql($map);
    }

    public function updateOnlySql(EntityMap $map, array $properties): string
    {
        $this->calls['updateOnly']++;

        return $this->inner->updateOnlySql($map, $properties);
    }

    public function deleteSql(EntityMap $map, bool $checkVersion): string
    {
        $this->calls['delete']++;

        return $this->inner->deleteSql($map, $checkVersion);
    }
}

/** A `Decimal`-keyed table: TEXT on SQLite, an exact numeric elsewhere. */
#[Table('perf_decimal_keyed')]
final class DecimalKeyed
{
    #[Key]
    #[Column]
    public Decimal $code;
}

/** A temporal-keyed table: never value-ordered by the rule, whatever the dialect stores. */
#[Table('perf_temporal_keyed')]
final class TemporalKeyed
{
    #[Key]
    #[Column]
    public DateTimeImmutable $at;
}
