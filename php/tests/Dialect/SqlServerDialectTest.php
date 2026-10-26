<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Dialect\SqlServerDialect;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\GuidDoc;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\NaturalKeyed;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\PropertyStub;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;
use SimpleOrm\Tests\Support\StorageMaps;

/**
 * Pins every string `SqlServerDialect` renders against `spec/dialects.md` (ADR-0024) — pure rendering, no database:
 * the metadata comes from the real loader over the sample entities, so the indexes and the audit columns are the
 * ones a live session would render. The live leg is {@see SqlServerLiveTest}.
 */
final class SqlServerDialectTest extends TestCase
{
    private SqlServerDialect $dialect;

    private EntityMapLoader $maps;

    protected function setUp(): void
    {
        $this->dialect = new SqlServerDialect();
        $this->maps = new EntityMapLoader();
    }

    #[Test]
    public function the_registry_resolves_sqlserver_case_insensitively(): void
    {
        self::assertInstanceOf(SqlServerDialect::class, Dialects::create('SqlServer'));
        self::assertContains('sqlserver', Dialects::names());
    }

    #[Test]
    public function create_table_sql_renders_a_database_generated_key_with_indexable_lengths_for_indexed_columns(): void
    {
        // email and display_name belong to declared indexes, so they take the indexable (450) length; name does not.
        self::assertSame(
            "if object_id(N'users', N'U') is null\n"
                . "create table [users] (\n"
                . "    [id] bigint identity(1,1) primary key,\n"
                . "    [name] nvarchar(max) not null,\n"
                . "    [email] nvarchar(450) not null,\n"
                . "    [display_name] nvarchar(450),\n"
                . "    [created_at] datetimeoffset not null,\n"
                . "    [updated_at] datetimeoffset\n"
                . ')',
            $this->dialect->createTableSql($this->maps->load(User::class)),
        );
    }

    #[Test]
    public function create_table_sql_renders_a_natural_composite_key_as_a_trailing_primary_key_line(): void
    {
        self::assertSame(
            "if object_id(N'user_roles', N'U') is null\n"
                . "create table [user_roles] (\n"
                . "    [user_id] bigint not null,\n"
                . "    [role_id] bigint not null,\n"
                . "    [granted_by] nvarchar(max),\n"
                . "    [created_at] datetimeoffset not null,\n"
                . "    [updated_at] datetimeoffset,\n"
                . "    primary key ([user_id], [role_id])\n"
                . ')',
            $this->dialect->createTableSql($this->maps->load(UserRole::class)),
        );
    }

    #[Test]
    public function create_table_sql_renders_a_client_guid_key_and_a_decimal_version_and_enum_columns(): void
    {
        self::assertSame(
            "if object_id(N'ss_guid_docs', N'U') is null\n"
                . "create table [ss_guid_docs] (\n"
                . "    [id] uniqueidentifier not null primary key,\n"
                . "    [title] nvarchar(max) not null\n"
                . ')',
            $this->dialect->createTableSql($this->maps->load(GuidDoc::class)),
        );

        // Decimal(38, 9), the version as bigint, the enum as nvarchar(100); status and created_at feed an index.
        self::assertSame(
            "if object_id(N'transactions', N'U') is null\n"
                . "create table [transactions] (\n"
                . "    [id] bigint identity(1,1) primary key,\n"
                . "    [user_id] bigint not null,\n"
                . "    [status] nvarchar(100) not null,\n"
                . "    [amount] decimal(18,2) not null,\n"
                . "    [version] bigint not null,\n"
                . "    [note] nvarchar(max),\n"
                . "    [created_at] datetimeoffset not null,\n"
                . "    [updated_at] datetimeoffset\n"
                . ')',
            $this->dialect->createTableSql($this->maps->load(Transaction::class)),
        );
    }

    #[Test]
    public function create_table_sql_gives_a_string_key_the_indexable_length_and_guards_with_the_unquoted_name(): void
    {
        $sql = $this->dialect->createTableSql($this->maps->load(NaturalKeyed::class));

        self::assertSame(
            "if object_id(N'it''s', N'U') is null\n"
                . "create table [it's] (\n"
                . "    [code] nvarchar(450) not null,\n"
                . "    [payload] varbinary(max),\n"
                . "    primary key ([code])\n"
                . ')',
            $sql,
        );
    }

    #[Test]
    public function create_index_sql_renders_the_guarded_form_with_descending_columns(): void
    {
        $indexes = $this->dialect->createIndexSql($this->maps->load(Transaction::class));

        self::assertCount(2, $indexes);
        self::assertSame(
            "if not exists (select 1 from sys.indexes where name = N'ix_transactions_user_id' and object_id = object_id(N'transactions'))\n"
                . 'create index [ix_transactions_user_id] on [transactions] ([user_id])',
            $indexes[0],
        );
        self::assertSame(
            "if not exists (select 1 from sys.indexes where name = N'ix_transactions_status_created' and object_id = object_id(N'transactions'))\n"
                . 'create index [ix_transactions_status_created] on [transactions] ([status], [created_at] desc)',
            $indexes[1],
        );

        $unique = $this->dialect->createIndexSql($this->maps->load(User::class));
        self::assertSame(
            "if not exists (select 1 from sys.indexes where name = N'ix_users_email' and object_id = object_id(N'users'))\n"
                . 'create unique index [ix_users_email] on [users] ([email])',
            $unique[0],
        );
    }

    #[Test]
    public function create_view_sql_is_create_or_alter(): void
    {
        $map = $this->maps->load(UserTransactionTotal::class);

        self::assertSame(
            'create or alter view [user_transaction_totals] as' . "\n" . $map->definingSql,
            $this->dialect->createViewSql($map),
        );
    }

    #[Test]
    public function insert_sql_reads_the_generated_key_back_with_scope_identity_in_the_same_batch(): void
    {
        self::assertSame(
            'insert into [users] ([name], [email], [display_name], [created_at], [updated_at])'
                . ' values (@name, @email, @display_name, @created_at, @updated_at)'
                . '; select cast(scope_identity() as bigint)',
            $this->dialect->insertSql($this->maps->load(User::class)),
        );
    }

    #[Test]
    public function insert_sql_has_no_key_read_back_for_a_natural_or_client_key(): void
    {
        self::assertSame(
            'insert into [user_roles] ([user_id], [role_id], [granted_by], [created_at], [updated_at])'
                . ' values (@user_id, @role_id, @granted_by, @created_at, @updated_at)',
            $this->dialect->insertSql($this->maps->load(UserRole::class)),
        );
        self::assertSame(
            'insert into [ss_guid_docs] ([id], [title]) values (@id, @title)',
            $this->dialect->insertSql($this->maps->load(GuidDoc::class)),
        );
    }

    #[Test]
    public function update_and_delete_render_the_batch_that_reports_its_own_count(): void
    {
        $transactions = $this->maps->load(Transaction::class);

        self::assertSame(
            'set nocount on; update [transactions] set [user_id] = @user_id, [status] = @status, [amount] = @amount, [note] = @note,'
                . ' [created_at] = @created_at, [updated_at] = @updated_at, [version] = [version] + 1'
                . ' where [id] = @id and [version] = @version; select @@rowcount; set nocount off',
            $this->dialect->updateSql($transactions),
        );
        self::assertSame(
            'set nocount on; update [transactions] set [status] = @status, [version] = [version] + 1'
                . ' where [id] = @id and [version] = @version; select @@rowcount; set nocount off',
            $this->dialect->updateOnlySql($transactions, [$transactions->property('status') ?? self::fail('status')]),
        );
        self::assertSame(
            'set nocount on; update [users] set [name] = @name, [email] = @email, [display_name] = @display_name,'
                . ' [created_at] = @created_at, [updated_at] = @updated_at where [id] = @id; select @@rowcount; set nocount off',
            $this->dialect->updateSql($this->maps->load(User::class)),
        );
        self::assertSame(
            'set nocount on; delete from [transactions] where [id] = @id; select @@rowcount; set nocount off',
            $this->dialect->deleteSql($transactions, checkVersion: false),
        );
        self::assertSame(
            'set nocount on; delete from [transactions] where [id] = @id and [version] = @version; select @@rowcount; set nocount off',
            $this->dialect->deleteSql($transactions, checkVersion: true),
        );
        self::assertSame(
            'set nocount on; delete from [user_roles] where [user_id] = @user_id and [role_id] = @role_id; select @@rowcount; set nocount off',
            $this->dialect->deleteSql($this->maps->load(UserRole::class), checkVersion: true),
        );
    }

    #[Test]
    public function the_dialect_reports_the_affected_rows_by_select_and_inserts_are_untouched(): void
    {
        self::assertTrue($this->dialect->reportsAffectedRowsBySelect());
        self::assertStringNotContainsString('nocount', $this->dialect->insertSql($this->maps->load(User::class)));
    }

    #[Test]
    public function limit_offset_clause_renders_the_three_forms_with_the_offset_first(): void
    {
        self::assertSame('offset @c1 rows fetch next @c0 rows only', $this->dialect->limitOffsetClause('@c0', '@c1'));
        self::assertSame('offset 0 rows fetch next @c0 rows only', $this->dialect->limitOffsetClause('@c0', null));
        self::assertSame('offset @c0 rows', $this->dialect->limitOffsetClause(null, '@c0'));
        self::assertSame('', $this->dialect->limitOffsetClause(null, null));
    }

    #[Test]
    public function quote_identifier_brackets_and_doubles_a_closing_bracket(): void
    {
        self::assertSame('[order]', $this->dialect->quoteIdentifier('order'));
        self::assertSame('[a]]b]', $this->dialect->quoteIdentifier('a]b'));
        self::assertSame('[a[b]', $this->dialect->quoteIdentifier('a[b'));
    }

    #[Test]
    public function migration_action_sql_matches_the_spec_strings(): void
    {
        self::assertSame("exec sp_rename N'users', N'people'", $this->dialect->renameTableSql('users', 'people'));
        self::assertSame("exec sp_rename N'o''neil', N'people'", $this->dialect->renameTableSql("o'neil", 'people'));
        self::assertSame(
            "exec sp_rename N'users.name', N'full_name', 'COLUMN'",
            $this->dialect->renameColumnSql('users', 'name', 'full_name'),
        );
        self::assertSame(
            'alter table [users] add [age] int not null default 0',
            $this->dialect->addColumnSql('users', 'age', 'int', false, '0'),
        );
        self::assertSame(
            'alter table [users] add [nick] nvarchar(max)',
            $this->dialect->addColumnSql('users', 'nick', 'nvarchar(max)', true, null),
        );
        // ADR-0043: the batch drops a leftover default constraint first (error 5074), byte for byte as spec/dialects.md gives it.
        self::assertSame(
            "declare @df sysname = (select dc.name from sys.default_constraints dc join sys.columns c on c.object_id = dc.parent_object_id "
                . "and c.column_id = dc.parent_column_id where dc.parent_object_id = object_id(N'users') and c.name = N'nick'); "
                . "if @df is not null exec(N'alter table [users] drop constraint ' + quotename(@df)); "
                . 'alter table [users] drop column [nick]',
            $this->dialect->dropColumnSql('users', 'nick'),
        );
        self::assertSame('drop table [users]', $this->dialect->dropTableSql('users'));
        self::assertSame('drop index [ix_users_name] on [users]', $this->dialect->dropIndexSql('users', 'ix_users_name'));
    }

    #[Test]
    public function version_table_sql_matches_the_spec_ddl_byte_for_byte(): void
    {
        self::assertSame(
            "if object_id(N'schema_version', N'U') is null\n"
                . "create table schema_version (\n"
                . "    version      bigint        not null,\n"
                . "    object       nvarchar(200) not null,\n"
                . "    description  nvarchar(400) not null,\n"
                . "    checksum     nvarchar(64)  not null,\n"
                . "    applied_at   nvarchar(40)  not null,\n"
                . "    execution_ms bigint        not null,\n"
                . "    primary key (version, object)\n"
                . ')',
            $this->dialect->versionTableSql(),
        );
    }

    #[Test]
    public function storage_type_covers_every_token(): void
    {
        $expected = [
            'int16' => 'smallint',
            'int32' => 'int',
            'int64' => 'bigint',
            'bool' => 'bit',
            'decimal' => 'decimal(38,9)',
            'double' => 'float',
            'float' => 'real',
            'string' => 'nvarchar(max)',
            'guid' => 'uniqueidentifier',
            'bytes' => 'varbinary(max)',
            'datetime' => 'datetimeoffset',
            'datetimeoffset' => 'datetimeoffset',
            'date' => 'date',
            'time' => 'time',
            'enum_text' => 'nvarchar(100)',
            'enum_int' => 'int',
            'custom' => 'nvarchar(max)',
        ];

        foreach (ColumnType::cases() as $type) {
            self::assertSame($expected[$type->value], $this->dialect->storageType(StorageMaps::of(self::property($type)), self::property($type)), "storage type of {$type->value}");
        }

        // A key column takes the indexable length; no other token changes.
        self::assertSame('nvarchar(450)', $this->dialect->storageType(StorageMaps::of(self::property(ColumnType::String, key: true)), self::property(ColumnType::String, key: true)));
        self::assertSame('nvarchar(450)', $this->dialect->storageType(StorageMaps::of(self::property(ColumnType::Custom, key: true)), self::property(ColumnType::Custom, key: true)));
        self::assertSame('varbinary(450)', $this->dialect->storageType(StorageMaps::of(self::property(ColumnType::Bytes, key: true)), self::property(ColumnType::Bytes, key: true)));
        self::assertSame('bigint', $this->dialect->storageType(StorageMaps::of(self::property(ColumnType::Int64, key: true)), self::property(ColumnType::Int64, key: true)));
    }

    #[Test]
    public function declared_type_compatibility_follows_the_val011_table_with_base_name_reduction(): void
    {
        $int = [ColumnType::Int32, ColumnType::Int64, ColumnType::Int16, ColumnType::EnumInt];
        $rows = [
            'int' => $int,
            'tinyint' => $int,
            'bigint' => [ColumnType::Int64, ColumnType::EnumInt],
            'smallint' => [ColumnType::Int16, ColumnType::Int32, ColumnType::Int64, ColumnType::EnumInt],
            'bit' => [ColumnType::Bool],
            'decimal' => [ColumnType::Decimal],
            'numeric' => [ColumnType::Decimal],
            'money' => [ColumnType::Decimal],
            'smallmoney' => [ColumnType::Decimal],
            'float' => [ColumnType::Double],
            'real' => [ColumnType::Float],
            'nvarchar' => [ColumnType::String, ColumnType::EnumText],
            'varchar' => [ColumnType::String, ColumnType::EnumText],
            'nchar' => [ColumnType::String, ColumnType::EnumText],
            'char' => [ColumnType::String, ColumnType::EnumText],
            'ntext' => [ColumnType::String],
            'text' => [ColumnType::String],
            'varbinary' => [ColumnType::Bytes],
            'binary' => [ColumnType::Bytes],
            'image' => [ColumnType::Bytes],
            'uniqueidentifier' => [ColumnType::Guid],
            'datetime' => [ColumnType::DateTime],
            'datetime2' => [ColumnType::DateTime],
            'smalldatetime' => [ColumnType::DateTime],
            'datetimeoffset' => [ColumnType::DateTime, ColumnType::DateTimeOffset],
            'date' => [ColumnType::DateTime, ColumnType::Date],
            'time' => [ColumnType::Time],
        ];

        foreach ($rows as $declared => $compatible) {
            foreach (ColumnType::cases() as $type) {
                self::assertSame(
                    in_array($type, $compatible, true),
                    $this->dialect->isDeclaredTypeCompatible($declared, $type),
                    "{$declared} vs {$type->value}",
                );
            }
        }

        // The base name: trimmed, lowercased, any parenthesized length or precision removed.
        self::assertTrue($this->dialect->isDeclaredTypeCompatible('decimal(18,2)', ColumnType::Decimal));
        self::assertTrue($this->dialect->isDeclaredTypeCompatible('NVARCHAR(100)', ColumnType::EnumText));
        self::assertTrue($this->dialect->isDeclaredTypeCompatible('  nvarchar(max) ', ColumnType::String));
        self::assertTrue($this->dialect->isDeclaredTypeCompatible('varbinary(450)', ColumnType::Bytes));
        self::assertFalse($this->dialect->isDeclaredTypeCompatible('decimal(18,2)', ColumnType::String));

        // An empty base name is compatible with nothing here; so is an unknown type and a custom token.
        self::assertFalse($this->dialect->isDeclaredTypeCompatible('', ColumnType::String));
        self::assertFalse($this->dialect->isDeclaredTypeCompatible('xml', ColumnType::String));
        self::assertFalse($this->dialect->isDeclaredTypeCompatible('nvarchar(max)', ColumnType::Custom));
    }

    #[Test]
    public function capability_flags_match_the_spec_table(): void
    {
        self::assertTrue($this->dialect->pagingRequiresOrderBy());
        self::assertFalse($this->dialect->supportsRowValueIn());
        self::assertFalse($this->dialect->supportsArrayParameters());
        self::assertFalse($this->dialect->bindsTemporalsNatively());
        self::assertFalse($this->dialect->supportsMaterializedViews());
        self::assertTrue($this->dialect->supportsProcedures());
        self::assertTrue($this->dialect->supportsTransactionalDdl());
        self::assertTrue($this->dialect->reportsAffectedRowsBySelect());
    }

    #[Test]
    public function a_planned_message_is_one_multi_statement_text_capped_at_2098_parameters(): void
    {
        self::assertTrue($this->dialect->supportsStatementBatching());
        self::assertSame(2098, $this->dialect->maxParametersPerMessage());
    }

    #[Test]
    public function introspection_queries_take_relation_and_name_their_result_columns(): void
    {
        $columns = $this->dialect->columnsInfoSql();
        self::assertStringContainsString('where c.object_id = object_id(@relation)', $columns);
        self::assertStringContainsString('as [type]', $columns);
        self::assertStringContainsString('as [notnull]', $columns);
        self::assertStringContainsString('as [pk]', $columns);
        self::assertStringContainsString('i.object_id = object_id(@relation)', $this->dialect->indexesInfoSql());
        foreach (['as [index_name]', 'as [unique]', 'as [seqno]', 'as [column]', 'as [desc]'] as $alias) {
            self::assertStringContainsString($alias, $this->dialect->indexesInfoSql());
        }

        self::assertStringContainsString('v.object_id = object_id(@relation)', $this->dialect->viewDefinitionSql());
        self::assertStringContainsString('as [definition]', $this->dialect->viewDefinitionSql());
        self::assertStringNotContainsString("\r", $columns . $this->dialect->indexesInfoSql() . $this->dialect->versionTableSql());
    }

    private static function property(ColumnType $type, bool $key = false): PropertyMap
    {
        return new PropertyMap(new ReflectionProperty(PropertyStub::class, 'value'), 'c', $type, null, nullable: false, key: $key);
    }
}
