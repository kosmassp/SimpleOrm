<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use DateTimeImmutable;
use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Parameters\ParameterBinder;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\SelectAst;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * Proves the storage layer end to end against a real SQLite file (ADR-0003,
 * CODING-STANDARD §7: no mocks, no fakes): every `createConnection` DSN form,
 * generated table DDL that actually executes, and a criteria SELECT rendered by
 * {@see \SimpleOrm\Query\AnsiSelectRenderer} whose parameters are bound by
 * {@see ParameterBinder} and {@see TypeConverter} and read back through PDO —
 * this area owns no session, so the plumbing is done by hand here exactly as a
 * future session would do it.
 */
final class SqliteRoundTripTest extends TestCase
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
    public function create_connection_normalizes_every_accepted_dsn_form_and_sets_pdo_attributes(): void
    {
        $dialect = new SqliteDialect();
        foreach ([
            $this->database->path,
            'sqlite:' . $this->database->path,
            'Data Source=' . $this->database->path,
        ] as $connectionString) {
            // setAttribute() would throw (ERRMODE_EXCEPTION is set first) if any
            // of createConnection()'s attributes were rejected; pdo_sqlite does
            // not support reading EMULATE_PREPARES/STRINGIFY_FETCHES back, so
            // only the retrievable ones are asserted directly.
            $pdo = $dialect->createConnection($connectionString);

            self::assertSame(PDO::ERRMODE_EXCEPTION, $pdo->getAttribute(PDO::ATTR_ERRMODE));
            self::assertSame(PDO::FETCH_ASSOC, $pdo->getAttribute(PDO::ATTR_DEFAULT_FETCH_MODE));

            $pdo->exec('create table if not exists probe (id INTEGER PRIMARY KEY) STRICT');
        }

        self::assertFileExists($this->database->path);
    }

    #[Test]
    public function generated_table_ddl_and_a_criteria_select_execute_against_real_sqlite(): void
    {
        $dialect = new SqliteDialect();
        $pdo = $dialect->createConnection($this->database->connectionString());
        $map = self::userMap();
        $converter = new TypeConverter(new TypeHandlerRegistry());

        $pdo->exec($dialect->createTableSql($map));

        self::insertUser($pdo, $converter, 'Ada', 'ada@example.com', null, '2026-01-01T00:00:00Z');
        self::insertUser($pdo, $converter, 'Grace', 'grace@example.com', 'Grace H.', '2026-01-02T00:00:00Z');

        $ast = new SelectAst($map, [Criteria::eq('Email', 'ada@example.com')], [], null, null);
        $parameters = [];
        $bind = static function (mixed $value, ?PropertyMap $property) use (&$parameters, $converter): string {
            $name = 'c' . count($parameters);
            $parameters[$name] = $converter->toDatabase($value, 'select users', $property?->enumAsInt() ?? false);

            return '@' . $name;
        };
        $sql = $dialect->selectSql($ast, $bind);

        self::assertSame(
            'select id, name, email, display_name, created_at, updated_at from users where email = @c0',
            $sql,
        );

        $statement = $pdo->prepare(SqlPlaceholders::toPdo($sql));
        PdoBinder::bindAndExecute($statement, $parameters);
        $rows = $statement->fetchAll();

        self::assertCount(1, $rows);
        self::assertSame('Ada', $rows[0]['name']);
        self::assertSame('ada@example.com', $rows[0]['email']);
        self::assertNull($rows[0]['display_name']);
        self::assertSame('2026-01-01T00:00:00.0000000Z', $rows[0]['created_at']);
    }

    private static function insertUser(
        PDO $pdo,
        TypeConverter $converter,
        string $name,
        string $email,
        ?string $displayName,
        string $createdAt,
    ): void {
        $args = new class ($name, $email, $displayName, new DateTimeImmutable($createdAt)) {
            public function __construct(
                public readonly string $name,
                public readonly string $email,
                public readonly ?string $displayName,
                public readonly DateTimeImmutable $createdAt,
            ) {
            }
        };

        $bound = ParameterBinder::bind(
            'insert into users (name, email, display_name, created_at) values (@Name, @Email, @DisplayName, @CreatedAt)',
            $args,
            'insert users',
            $converter,
        );
        PdoBinder::bindAndExecute($pdo->prepare($bound->sql), $bound->parameters);
    }

    private static function userMap(): EntityMap
    {
        return new EntityMap(
            User::class,
            RelationKind::Table,
            'users',
            null,
            null,
            [],
            [
                self::prop('id', 'id', ColumnType::Int64, 'int', key: true, generated: true),
                self::prop('name', 'name', ColumnType::String, 'string'),
                self::prop('email', 'email', ColumnType::String, 'string'),
                self::prop('displayName', 'display_name', ColumnType::String, 'string', nullable: true),
                self::prop('createdAtUtc', 'created_at', ColumnType::DateTime, DateTimeImmutable::class),
                self::prop('updatedAtUtc', 'updated_at', ColumnType::DateTime, DateTimeImmutable::class, nullable: true),
            ],
            KeyStrategy::DatabaseGenerated,
            [],
            [],
        );
    }

    private static function prop(
        string $property,
        string $column,
        ColumnType $type,
        string $phpType,
        bool $nullable = false,
        bool $key = false,
        bool $generated = false,
    ): PropertyMap {
        return new PropertyMap(new ReflectionProperty(User::class, $property), $column, $type, $phpType, $nullable, $key, $generated);
    }
}
