<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use PDO;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Dialect\PdoDsn;
use SimpleOrm\Dialect\PostgresDialect;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Dialect\SqlServerDialect;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\IndexColumn;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaColumn;
use SimpleOrm\Migrations\TableSchemaIndex;
use SimpleOrm\Migrations\TableSchemaIndexPart;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\PropertyStub;
use SimpleOrm\Tests\Support\StorageMaps;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * The seam members ADR-0043 added for migration tooling beyond SQLite (spec/dialects.md "The seam, member by member",
 * "Storage types", "Migration-action DDL"): the dialect's name, the storage type of a property with its map and facets,
 * the snapshot-driven renderings, the view members and the verbatim flag, and the fifth `generated` column of the
 * columns query — pure strings except the one live SQLite introspection.
 */
final class MigrationToolingSeamTest extends TestCase
{
    #[Test]
    public function every_dialect_names_itself_as_the_cli_spells_it(): void
    {
        self::assertSame('sqlite', (new SqliteDialect())->name());
        self::assertSame('sqlserver', (new SqlServerDialect())->name());
        self::assertSame('postgres', (new PostgresDialect())->name());
        foreach (Dialects::names() as $name) {
            self::assertSame($name, Dialects::create($name)->name());
        }
    }

    #[Test]
    public function only_postgresql_reformats_a_stored_view(): void
    {
        self::assertTrue((new SqliteDialect())->viewDefinitionIsVerbatim());
        self::assertTrue((new SqlServerDialect())->viewDefinitionIsVerbatim());
        self::assertFalse((new PostgresDialect())->viewDefinitionIsVerbatim());
    }

    // --- storage types with the map and the facets -----------------------------------------

    #[Test]
    public function sql_server_gives_an_indexed_column_the_indexable_length_like_a_key(): void
    {
        $dialect = new SqlServerDialect();
        $column = self::property(ColumnType::String, 'code');
        $indexed = StorageMaps::of($column, [new EntityIndex('ix_t_code', [new IndexColumn('value', 'code', false)], false)]);

        self::assertSame('nvarchar(450)', $dialect->storageType($indexed, $column));
        self::assertSame('nvarchar(max)', $dialect->storageType(StorageMaps::of($column), $column));
        self::assertSame('varbinary(450)', $dialect->storageType(
            StorageMaps::of(self::property(ColumnType::Bytes, 'code'), [new EntityIndex('ix_t_code', [new IndexColumn('value', 'code', false)], false)]),
            self::property(ColumnType::Bytes, 'code'),
        ));
    }

    #[Test]
    public function a_length_facet_wins_over_the_default_and_the_indexable_length_on_sql_server(): void
    {
        $dialect = new SqlServerDialect();
        $key = self::property(ColumnType::String, 'code', key: true, length: 64);
        $bytes = self::property(ColumnType::Bytes, 'digest', length: 16);

        self::assertSame('nvarchar(64)', $dialect->storageType(StorageMaps::of($key), $key));
        self::assertSame('varbinary(16)', $dialect->storageType(StorageMaps::of($bytes), $bytes));
    }

    #[Test]
    public function a_precision_facet_renders_decimal_p_s_without_a_space_and_the_default_is_38_9(): void
    {
        $dialect = new SqlServerDialect();
        $declared = self::property(ColumnType::Decimal, 'amount', precision: 18, scale: 2);
        $whole = self::property(ColumnType::Decimal, 'whole', precision: 10, scale: 0);
        $default = self::property(ColumnType::Decimal, 'plain');

        self::assertSame('decimal(18,2)', $dialect->storageType(StorageMaps::of($declared), $declared));
        self::assertSame('decimal(10,0)', $dialect->storageType(StorageMaps::of($whole), $whole));
        self::assertSame('decimal(38,9)', $dialect->storageType(StorageMaps::of($default), $default));
    }

    #[Test]
    public function postgresql_renders_numeric_p_s_and_character_varying_n_and_ignores_a_bytea_length(): void
    {
        $dialect = new PostgresDialect();
        $amount = self::property(ColumnType::Decimal, 'amount', precision: 18, scale: 2);
        $plain = self::property(ColumnType::Decimal, 'plain');
        $label = self::property(ColumnType::String, 'label', length: 50);
        $bare = self::property(ColumnType::String, 'bare');
        $digest = self::property(ColumnType::Bytes, 'digest', length: 16);

        self::assertSame('numeric(18,2)', $dialect->storageType(StorageMaps::of($amount), $amount));
        self::assertSame('numeric', $dialect->storageType(StorageMaps::of($plain), $plain));
        self::assertSame('character varying(50)', $dialect->storageType(StorageMaps::of($label), $label));
        self::assertSame('text', $dialect->storageType(StorageMaps::of($bare), $bare));
        self::assertSame('bytea', $dialect->storageType(StorageMaps::of($digest), $digest));
    }

    #[Test]
    public function sqlite_storage_classes_carry_no_size_and_ignore_the_facets(): void
    {
        $dialect = new SqliteDialect();
        $amount = self::property(ColumnType::Decimal, 'amount', precision: 18, scale: 2);
        $label = self::property(ColumnType::String, 'label', length: 50);
        $digest = self::property(ColumnType::Bytes, 'digest', length: 16);

        self::assertSame('TEXT', $dialect->storageType(StorageMaps::of($amount), $amount));
        self::assertSame('TEXT', $dialect->storageType(StorageMaps::of($label), $label));
        self::assertSame('BLOB', $dialect->storageType(StorageMaps::of($digest), $digest));
    }

    #[Test]
    public function the_metadata_rendered_create_table_uses_the_same_storage_type_function(): void
    {
        $map = (new \SimpleOrm\Metadata\EntityMapLoader())->load(\SimpleOrm\Tests\Sample\Models\User::class);

        $sql = (new SqlServerDialect())->createTableSql($map);

        self::assertStringContainsString('[email] nvarchar(450) not null', $sql, 'an indexed string renders nvarchar(450)');
        self::assertStringContainsString('[display_name] nvarchar(450)', $sql);
        self::assertStringContainsString('[name] nvarchar(max) not null', $sql);
    }

    // --- snapshot-driven renderings ---------------------------------------------------------

    #[Test]
    public function a_snapshot_table_renders_per_dialect_with_the_generated_key_inline(): void
    {
        $schema = new TableSchema('widgets', [
            new TableSchemaColumn('id', 'bigint', false, key: true, generated: true),
            new TableSchemaColumn('name', 'text', false),
            new TableSchemaColumn('note', 'text', true),
        ], []);

        self::assertSame(
            "create table if not exists widgets (\n    id INTEGER PRIMARY KEY,\n    name text NOT NULL,\n    note text\n) STRICT",
            (new SqliteDialect())->createTableSqlFromSchema($schema),
        );
        self::assertSame(
            "if object_id(N'widgets', N'U') is null\ncreate table [widgets] (\n    [id] bigint identity(1,1) primary key,\n    [name] text not null,\n    [note] text\n)",
            (new SqlServerDialect())->createTableSqlFromSchema($schema),
        );
        self::assertSame(
            "create table if not exists \"widgets\" (\n    \"id\" bigint generated by default as identity primary key,\n    \"name\" text not null,\n    \"note\" text\n)",
            (new PostgresDialect())->createTableSqlFromSchema($schema),
        );
    }

    #[Test]
    public function a_composite_key_renders_as_the_trailing_constraint(): void
    {
        $schema = new TableSchema('links', [
            new TableSchemaColumn('a', 'bigint', false, key: true),
            new TableSchemaColumn('b', 'bigint', false, key: true),
        ], []);

        self::assertStringEndsWith("    b bigint NOT NULL,\n    primary key (a, b)\n) STRICT", (new SqliteDialect())->createTableSqlFromSchema($schema));
        self::assertStringEndsWith("    [b] bigint not null,\n    primary key ([a], [b])\n)", (new SqlServerDialect())->createTableSqlFromSchema($schema));
        self::assertStringEndsWith("    \"b\" bigint not null,\n    primary key (\"a\", \"b\")\n)", (new PostgresDialect())->createTableSqlFromSchema($schema));
    }

    #[Test]
    public function a_snapshot_index_renders_per_dialect(): void
    {
        $index = new TableSchemaIndex('ix_widgets_name', [new TableSchemaIndexPart('name'), new TableSchemaIndexPart('note', true)], true);

        self::assertSame(
            'create unique index if not exists ix_widgets_name on widgets (name, note desc)',
            (new SqliteDialect())->createIndexSqlFromSchema('widgets', $index),
        );
        self::assertSame(
            "if not exists (select 1 from sys.indexes where name = N'ix_widgets_name' and object_id = object_id(N'widgets'))\n"
                . 'create unique index [ix_widgets_name] on [widgets] ([name], [note] desc)',
            (new SqlServerDialect())->createIndexSqlFromSchema('widgets', $index),
        );
        self::assertSame(
            'create unique index if not exists "ix_widgets_name" on "widgets" ("name", "note" desc)',
            (new PostgresDialect())->createIndexSqlFromSchema('widgets', $index),
        );
    }

    #[Test]
    public function a_view_renders_from_its_neutral_parts_and_drops_idempotently(): void
    {
        $body = 'select 1 as answer';

        self::assertSame("create view if not exists totals as\n{$body}", (new SqliteDialect())->createViewSqlFromBody('totals', $body, false));
        self::assertSame("create or alter view [totals] as\n{$body}", (new SqlServerDialect())->createViewSqlFromBody('totals', $body, false));
        self::assertSame("create or replace view \"totals\" as\n{$body}", (new PostgresDialect())->createViewSqlFromBody('totals', $body, false));
        self::assertSame("create materialized view if not exists \"totals\" as\n{$body}", (new PostgresDialect())->createViewSqlFromBody('totals', $body, true));

        self::assertSame('drop view if exists totals', (new SqliteDialect())->dropViewSql('totals', false));
        self::assertSame('drop view if exists [totals]', (new SqlServerDialect())->dropViewSql('totals', false));
        self::assertSame('drop view if exists "totals"', (new PostgresDialect())->dropViewSql('totals', false));
        self::assertSame('drop materialized view if exists "totals"', (new PostgresDialect())->dropViewSql('totals', true));
    }

    #[Test]
    public function the_metadata_view_render_is_the_parts_render_for_the_same_name_and_body(): void
    {
        $map = (new \SimpleOrm\Metadata\EntityMapLoader())->load(\SimpleOrm\Tests\Sample\Models\UserTransactionTotal::class);

        foreach (Dialects::names() as $name) {
            $dialect = Dialects::create($name);
            self::assertSame(
                $dialect->createViewSqlFromBody('user_transaction_totals', (string) $map->definingSql, false),
                $dialect->createViewSql($map),
                $name,
            );
        }
    }

    // --- introspection ----------------------------------------------------------------------

    /** @return iterable<string, array{Dialect}> */
    public static function dialects(): iterable
    {
        foreach (Dialects::names() as $name) {
            yield $name => [Dialects::create($name)];
        }
    }

    #[Test]
    #[DataProvider('dialects')]
    public function the_columns_query_reports_a_fifth_generated_column(Dialect $dialect): void
    {
        $sql = $dialect->columnsInfoSql();

        self::assertMatchesRegularExpression('/generated/i', $sql);
        self::assertStringNotContainsString("\r", $sql);
    }

    #[Test]
    public function sqlite_reports_a_lone_integer_primary_key_as_generated(): void
    {
        $database = TempDatabase::create();
        $dialect = new SqliteDialect();
        $connection = $dialect->createConnection($database->connectionString());

        try {
            $connection->exec('create table single_key (id INTEGER PRIMARY KEY, label TEXT NOT NULL) STRICT');
            $connection->exec('create table pair_key (a INTEGER NOT NULL, b INTEGER NOT NULL, primary key (a, b)) STRICT');
            $connection->exec('create table text_key (code TEXT NOT NULL PRIMARY KEY, n INTEGER) STRICT');

            self::assertSame(
                [['id', 'INTEGER', 0, 1, 1], ['label', 'TEXT', 1, 0, 0]],
                self::columns($connection, $dialect, 'single_key'),
            );
            self::assertSame([0, 0], array_column(self::columns($connection, $dialect, 'pair_key'), 4), 'a composite key is never generated');
            self::assertSame([0, 0], array_column(self::columns($connection, $dialect, 'text_key'), 4), 'only an INTEGER key aliases the rowid');
        } finally {
            $connection = null;
            $database->delete();
        }
    }

    // --- the DSN editor ---------------------------------------------------------------------

    #[Test]
    public function the_dsn_editor_replaces_a_key_in_any_position_and_appends_a_missing_one(): void
    {
        self::assertSame(
            'pgsql:host=h;port=5432;dbname=shadow;user=u',
            PdoDsn::withValue('pgsql:host=h;port=5432;dbname=postgres;user=u', 'dbname', 'shadow'),
        );
        self::assertSame('pgsql:dbname=shadow;host=h', PdoDsn::withValue('pgsql:dbname=postgres;host=h', 'dbname', 'shadow'), 'also the first entry behind the driver prefix');
        self::assertSame('sqlsrv:Server=s;Database=shadow', PdoDsn::withValue('sqlsrv:Server=s;Database=master', 'database', 'shadow'), 'case-insensitive; keeps the key as given');
        self::assertSame('odbc:Driver={X};Server=s;Database=shadow', PdoDsn::withValue('odbc:Driver={X};Server=s', 'Database', 'shadow'));
    }

    // --- helpers ----------------------------------------------------------------------------

    private static function property(
        ColumnType $type,
        string $column,
        bool $key = false,
        ?int $length = null,
        ?int $precision = null,
        ?int $scale = null,
    ): PropertyMap {
        return new PropertyMap(
            new ReflectionProperty(PropertyStub::class, 'value'),
            $column,
            $type,
            null,
            nullable: false,
            key: $key,
            length: $length,
            precision: $precision,
            scale: $scale,
        );
    }

    /** @return list<array{0: string, 1: string, 2: int, 3: int, 4: int}> name, type, notnull, pk, generated */
    private static function columns(PDO $connection, Dialect $dialect, string $relation): array
    {
        $statement = $connection->prepare(SqlPlaceholders::toPdo($dialect->columnsInfoSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $relation]);

        return array_map(
            static fn (array $row): array => [(string) $row[0], (string) $row[1], (int) $row[2], (int) $row[3], (int) $row[4]],
            $statement->fetchAll(PDO::FETCH_NUM),
        );
    }
}
