<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\IndexColumn;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionStatus;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserRole;

/**
 * Pins every rendered DDL/CRUD/action string of {@see SqliteDialect} against the
 * exact strings in `dotnet/src/SimpleOrm.Sqlite/SqliteDialect.cs`
 * (CODING-STANDARD §5/§10 checklist: "SQLite SQL byte-identical to the
 * reference"). `EntityMap`s are hand-built fixtures — the metadata loader is a
 * different area's work.
 */
final class SqliteDialectTest extends TestCase
{
    private SqliteDialect $dialect;

    protected function setUp(): void
    {
        $this->dialect = new SqliteDialect();
    }

    #[Test]
    public function create_table_sql_renders_a_database_generated_key(): void
    {
        $sql = $this->dialect->createTableSql(self::userMap());

        self::assertSame(
            "create table if not exists users (\n"
                . "    id INTEGER PRIMARY KEY,\n"
                . "    name TEXT NOT NULL,\n"
                . "    email TEXT NOT NULL,\n"
                . "    display_name TEXT,\n"
                . "    created_at TEXT NOT NULL,\n"
                . "    updated_at TEXT\n"
                . ') STRICT',
            $sql,
        );
    }

    #[Test]
    public function create_table_sql_renders_a_natural_composite_key(): void
    {
        $sql = $this->dialect->createTableSql(self::userRoleMap());

        self::assertSame(
            "create table if not exists user_roles (\n"
                . "    user_id INTEGER NOT NULL,\n"
                . "    role_id INTEGER NOT NULL,\n"
                . "    granted_by TEXT,\n"
                . "    created_at TEXT NOT NULL,\n"
                . "    updated_at TEXT,\n"
                . "    primary key (user_id, role_id)\n"
                . ') STRICT',
            $sql,
        );
    }

    #[Test]
    public function create_table_sql_renders_a_client_guid_key(): void
    {
        // Test-local fixture (CODING-STANDARD §7): an anonymous class stands in
        // for a client-GUID-keyed entity, reflected from its own instance.
        $widget = new class {
            public string $id;
        };

        $map = new EntityMap(
            $widget::class,
            RelationKind::Table,
            'widgets',
            null,
            null,
            [],
            [new PropertyMap(new ReflectionProperty($widget, 'id'), 'id', ColumnType::Guid, 'string', nullable: false, key: true)],
            KeyStrategy::ClientGuid,
            [],
            [],
        );

        self::assertSame(
            "create table if not exists widgets (\n    id TEXT NOT NULL PRIMARY KEY\n) STRICT",
            $this->dialect->createTableSql($map),
        );
    }

    #[Test]
    public function create_index_sql_renders_a_unique_single_column_index(): void
    {
        $map = self::withIndexes(self::userMap(), [
            new EntityIndex('ix_users_email', [new IndexColumn('email', 'email', false)], unique: true),
        ]);

        self::assertSame(
            ['create unique index if not exists ix_users_email on users (email)'],
            $this->dialect->createIndexSql($map),
        );
    }

    #[Test]
    public function create_index_sql_renders_a_non_unique_composite_index_with_a_descending_column(): void
    {
        $map = self::withIndexes(self::transactionMap(), [
            new EntityIndex(
                'ix_transactions_status_created',
                [new IndexColumn('status', 'status', false), new IndexColumn('createdAtUtc', 'created_at', true)],
                unique: false,
            ),
        ]);

        self::assertSame(
            ['create index if not exists ix_transactions_status_created on transactions (status, created_at desc)'],
            $this->dialect->createIndexSql($map),
        );
    }

    /** @param list<EntityIndex> $indexes */
    private static function withIndexes(EntityMap $map, array $indexes): EntityMap
    {
        return new EntityMap(
            $map->entityType,
            $map->kind,
            $map->relationName,
            $map->schema,
            $map->definingSql,
            $map->statementParameters,
            $map->properties,
            $map->keyStrategy,
            $indexes,
            $map->relationships,
        );
    }

    #[Test]
    public function insert_sql_excludes_generated_columns_and_returns_the_key(): void
    {
        self::assertSame(
            'insert into users (name, email, display_name, created_at, updated_at) '
                . 'values (@name, @email, @display_name, @created_at, @updated_at) returning id',
            $this->dialect->insertSql(self::userMap()),
        );
    }

    #[Test]
    public function insert_sql_has_no_returning_clause_for_a_natural_key(): void
    {
        self::assertSame(
            'insert into user_roles (user_id, role_id, granted_by, created_at, updated_at) '
                . 'values (@user_id, @role_id, @granted_by, @created_at, @updated_at)',
            $this->dialect->insertSql(self::userRoleMap()),
        );
    }

    #[Test]
    public function update_sql_bumps_the_version_and_requires_it_in_the_where(): void
    {
        self::assertSame(
            'update transactions set user_id = @user_id, status = @status, amount = @amount, '
                . 'note = @note, created_at = @created_at, updated_at = @updated_at, version = version + 1 '
                . 'where id = @id and version = @version',
            $this->dialect->updateSql(self::transactionMap()),
        );
    }

    #[Test]
    public function update_sql_without_a_version_column_has_a_plain_key_where(): void
    {
        self::assertSame(
            'update users set name = @name, email = @email, display_name = @display_name, '
                . 'created_at = @created_at, updated_at = @updated_at where id = @id',
            $this->dialect->updateSql(self::userMap()),
        );
    }

    #[Test]
    public function delete_sql_without_version_check_is_a_plain_key_where(): void
    {
        self::assertSame(
            'delete from transactions where id = @id',
            $this->dialect->deleteSql(self::transactionMap(), checkVersion: false),
        );
    }

    #[Test]
    public function delete_sql_with_version_check_adds_the_version_predicate(): void
    {
        self::assertSame(
            'delete from transactions where id = @id and version = @version',
            $this->dialect->deleteSql(self::transactionMap(), checkVersion: true),
        );
    }

    #[Test]
    public function limit_offset_clause_covers_every_combination(): void
    {
        self::assertSame('limit @c0 offset @c1', $this->dialect->limitOffsetClause('@c0', '@c1'));
        self::assertSame('limit @c0', $this->dialect->limitOffsetClause('@c0', null));
        // SQLite requires LIMIT before OFFSET.
        self::assertSame('limit -1 offset @c0', $this->dialect->limitOffsetClause(null, '@c0'));
        self::assertSame('', $this->dialect->limitOffsetClause(null, null));
    }

    #[Test]
    public function version_table_sql_matches_the_reference_ddl(): void
    {
        self::assertSame(
            "create table if not exists schema_version (\n"
                . "    version      INTEGER NOT NULL,\n"
                . "    object       TEXT NOT NULL,\n"
                . "    description  TEXT NOT NULL,\n"
                . "    checksum     TEXT NOT NULL,\n"
                . "    applied_at   TEXT NOT NULL,\n"
                . "    execution_ms INTEGER NOT NULL,\n"
                . "    primary key (version, object)\n"
                . ') STRICT',
            $this->dialect->versionTableSql(),
        );
    }

    #[Test]
    public function migration_action_sql_matches_the_reference_strings(): void
    {
        self::assertSame('alter table users rename to people', $this->dialect->renameTableSql('users', 'people'));
        self::assertSame(
            'alter table users rename column name to full_name',
            $this->dialect->renameColumnSql('users', 'name', 'full_name'),
        );
        self::assertSame(
            'alter table users add column age INTEGER',
            $this->dialect->addColumnSql('users', 'age', 'INTEGER', nullable: true, defaultSql: null),
        );
        self::assertSame(
            'alter table users add column age INTEGER not null',
            $this->dialect->addColumnSql('users', 'age', 'INTEGER', nullable: false, defaultSql: null),
        );
        self::assertSame(
            'alter table users add column age INTEGER not null default 0',
            $this->dialect->addColumnSql('users', 'age', 'INTEGER', nullable: false, defaultSql: '0'),
        );
        self::assertSame('alter table users drop column age', $this->dialect->dropColumnSql('users', 'age'));
        self::assertSame('drop table users', $this->dialect->dropTableSql('users'));
        self::assertSame('drop index ix_users_email', $this->dialect->dropIndexSql('users', 'ix_users_email'));
    }

    // --- fixtures ---------------------------------------------------------------------

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
                self::prop(User::class, 'id', 'id', ColumnType::Int64, 'int', key: true, generated: true),
                self::prop(User::class, 'name', 'name', ColumnType::String, 'string'),
                self::prop(User::class, 'email', 'email', ColumnType::String, 'string'),
                self::prop(User::class, 'displayName', 'display_name', ColumnType::String, 'string', nullable: true),
                self::prop(User::class, 'createdAtUtc', 'created_at', ColumnType::DateTime, DateTimeImmutable::class),
                self::prop(User::class, 'updatedAtUtc', 'updated_at', ColumnType::DateTime, DateTimeImmutable::class, nullable: true),
            ],
            KeyStrategy::DatabaseGenerated,
            [],
            [],
        );
    }

    private static function userRoleMap(): EntityMap
    {
        return new EntityMap(
            UserRole::class,
            RelationKind::Table,
            'user_roles',
            null,
            null,
            [],
            [
                self::prop(UserRole::class, 'userId', 'user_id', ColumnType::Int64, 'int', key: true),
                self::prop(UserRole::class, 'roleId', 'role_id', ColumnType::Int64, 'int', key: true),
                self::prop(UserRole::class, 'grantedBy', 'granted_by', ColumnType::String, 'string', nullable: true),
                self::prop(UserRole::class, 'createdAtUtc', 'created_at', ColumnType::DateTime, DateTimeImmutable::class),
                self::prop(UserRole::class, 'updatedAtUtc', 'updated_at', ColumnType::DateTime, DateTimeImmutable::class, nullable: true),
            ],
            KeyStrategy::Natural,
            [],
            [],
        );
    }

    private static function transactionMap(): EntityMap
    {
        $version = self::prop(Transaction::class, 'version', 'version', ColumnType::Int64, 'int', version: true);

        return new EntityMap(
            Transaction::class,
            RelationKind::Table,
            'transactions',
            null,
            null,
            [],
            [
                self::prop(Transaction::class, 'id', 'id', ColumnType::Int64, 'int', key: true, generated: true),
                self::prop(Transaction::class, 'userId', 'user_id', ColumnType::Int64, 'int'),
                self::prop(Transaction::class, 'status', 'status', ColumnType::EnumText, TransactionStatus::class),
                self::prop(Transaction::class, 'amount', 'amount', ColumnType::Decimal, 'string'),
                $version,
                self::prop(Transaction::class, 'note', 'note', ColumnType::String, 'string', nullable: true),
                self::prop(Transaction::class, 'createdAtUtc', 'created_at', ColumnType::DateTime, DateTimeImmutable::class),
                self::prop(Transaction::class, 'updatedAtUtc', 'updated_at', ColumnType::DateTime, DateTimeImmutable::class, nullable: true),
            ],
            KeyStrategy::DatabaseGenerated,
            [],
            [],
        );
    }

    /** @param class-string $class */
    private static function prop(
        string $class,
        string $property,
        string $column,
        ColumnType $type,
        string $phpType,
        bool $nullable = false,
        bool $key = false,
        bool $generated = false,
        bool $version = false,
    ): PropertyMap {
        return new PropertyMap(
            new ReflectionProperty($class, $property),
            $column,
            $type,
            $phpType,
            $nullable,
            $key,
            $generated,
            $version,
        );
    }
}
