<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Discovery\ClassScanner;
use SimpleOrm\Errors\SchemaValidationException;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\MappingOptions;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Metadata\StatementParameter;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Tests\Validation\Fixtures\BadRegistry;
use SimpleOrm\Tests\Validation\Fixtures\BadStatementEntity;
use SimpleOrm\Tests\Validation\Fixtures\GhostColumnEntity;
use SimpleOrm\Tests\Validation\Fixtures\MissingRelationEntity;
use SimpleOrm\Validation\SchemaGuard;

/**
 * Milestone 6 (PHP port): SchemaGuard — every rule has a failing fixture and
 * the report names the source, mirroring `SchemaGuardTests.cs`.
 */
final class SchemaGuardTest extends TestCase
{
    private TempDatabase $database;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
    }

    protected function tearDown(): void
    {
        $this->database->delete();
    }

    #[Test]
    public function the_sample_validates_clean(): void
    {
        $db = SampleDatabase::open($this->database);
        $classes = ClassScanner::classes(__DIR__ . '/../Sample/Models', 'SimpleOrm\\Tests\\Sample\\Models');

        SchemaGuard::validate($db, $classes);   // no throw

        self::assertNotEmpty($classes);
    }

    #[Test]
    public function missing_migrations_are_mig_030(): void
    {
        // A fresh database: no schema, no applied migrations.
        $db = Db::open($this->database->connectionString(), SampleDatabase::options());
        $widgets = new SqlVersion(1, new SqlVersionStep(
            'widgets',
            'create',
            ['create table widgets (id INTEGER PRIMARY KEY) STRICT'],
            ['drop table widgets'],
        ));

        try {
            SchemaGuard::validate($db, [User::class], MigrationSet::of($widgets));
            self::fail('expected SchemaValidationException');
        } catch (SchemaValidationException $exception) {
            $codes = array_map(static fn ($e) => $e->code, $exception->errors);
            self::assertContains('MIG-030', $codes);
            self::assertContains('VAL-012', $codes);   // the entity's table is also missing
        }
    }

    #[Test]
    public function every_rule_fires_with_its_code_in_one_report(): void
    {
        $db = SampleDatabase::open($this->database);

        $errors = SchemaGuard::report($db, [BadRegistry::class, MissingRelationEntity::class, GhostColumnEntity::class]);

        self::assertTrue(self::has($errors, 'VAL-001', 'BadRegistry.badSql'));
        self::assertTrue(self::has($errors, 'VAL-021', 'BadRegistry.star'));
        self::assertFalse(self::hasSource($errors, 'BadRegistry.countStarIsFine'));
        self::assertTrue(self::has($errors, 'PRM-001', 'BadRegistry.wrongParams'));
        self::assertTrue(self::has($errors, 'PRM-002', 'BadRegistry.wrongParams'));
        self::assertTrue(self::has($errors, 'MAP-001', 'BadRegistry.wrongShape'));
        self::assertTrue(self::has($errors, 'VAL-010', 'BadRegistry.nullableIntoNonNullable'));
        self::assertTrue(self::has($errors, 'VAL-010', 'BadRegistry.expressionNeedsNullable'));
        self::assertFalse(self::hasSource($errors, 'BadRegistry.expressionWithComment'));
        self::assertTrue(self::has($errors, 'VAL-011', 'BadRegistry.declaredTypeMismatch'));
        self::assertTrue(self::has($errors, 'VAL-020', 'BadRegistry.nowLint'));
        self::assertTrue(self::has($errors, 'VAL-012', 'MissingRelationEntity'));
        self::assertTrue(self::has($errors, 'VAL-013', 'GhostColumnEntity'));

        // The report is complete and every source is named, not first-error-only.
        self::assertGreaterThanOrEqual(11, count($errors));

        $exception = new SchemaValidationException($errors);
        self::assertStringContainsString('BadRegistry.wrongParams', $exception->getMessage());
    }

    #[Test]
    public function validation_leaves_no_trace(): void
    {
        $db = SampleDatabase::open($this->database);
        $user = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');

        // nowLint is a Command (UPDATE); SchemaGuard must prepare it without executing it.
        SchemaGuard::report($db, [BadRegistry::class]);

        $reloaded = $db->get(User::class, $user->id);
        self::assertNull($reloaded->updatedAtUtc);
    }

    #[Test]
    public function statement_entity_declared_parameter_mismatch_is_prm_012(): void
    {
        $property = new PropertyMap(new ReflectionProperty(BadStatementEntity::class, 'one'), 'one', ColumnType::Int64, 'int', nullable: false);
        $brokenMap = new EntityMap(
            BadStatementEntity::class,
            RelationKind::Statement,
            relationName: null,
            schema: null,
            definingSql: 'select 1 as one where 1 = @used',
            statementParameters: [new StatementParameter('unused', ColumnType::Int64)],
            properties: [$property],
            keyStrategy: KeyStrategy::None,
            indexes: [],
            relationships: [],
        );

        $db = Db::open($this->database->connectionString(), new DbOptions(
            new SqliteDialect(),
            new MappingOptions(explicitMaps: [BadStatementEntity::class => $brokenMap]),
        ));

        $errors = SchemaGuard::report($db, [BadStatementEntity::class]);

        $prm012 = array_values(array_filter($errors, static fn ($e) => $e->code === 'PRM-012'));
        self::assertCount(2, $prm012);
        foreach ($prm012 as $error) {
            self::assertSame('BadStatementEntity [Statement]', $error->target);
        }
    }

    /** @param list<\SimpleOrm\Errors\ValidationError> $errors */
    private static function has(array $errors, string $code, string $target): bool
    {
        foreach ($errors as $error) {
            if ($error->code === $code && $error->target === $target) {
                return true;
            }
        }

        return false;
    }

    /** @param list<\SimpleOrm\Errors\ValidationError> $errors */
    private static function hasSource(array $errors, string $target): bool
    {
        foreach ($errors as $error) {
            if ($error->target === $target) {
                return true;
            }
        }

        return false;
    }
}
