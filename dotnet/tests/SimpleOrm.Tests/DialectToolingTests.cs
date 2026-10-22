using SimpleOrm.Postgres;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Migration tooling beyond SQLite (ADR-0043): the snapshot stamp and its
/// <c>MIG-014</c> refusal, the dialect-neutral view normalizer, and the seam
/// members the derived rollback, the shadow baseline, and the generator render
/// through on every dialect — pinned without an engine, as the renderings are.
/// </summary>
public sealed class DialectToolingTests
{
    private static readonly EntityMapLoader Loader = new();

    [Fact]
    public void Non_sqlite_snapshots_carry_the_dialect_stamp_after_the_object_and_sqlite_ones_stay_unstamped()
    {
        var users = Loader.Load<User>();
        var at = new DateTimeOffset(2026, 8, 29, 12, 0, 0, TimeSpan.Zero);

        var sqlite = SchemaSnapshot.Export(users, new SqliteDialect(), 7, at);
        Assert.DoesNotContain("\"dialect\"", sqlite);
        Assert.Equal("sqlite", SchemaSnapshot.DialectOf(sqlite));

        var postgres = SchemaSnapshot.Export(users, new PostgresDialect(), 7, at).Replace("\r", string.Empty);
        Assert.Contains("\"object\": \"users\",\n  \"dialect\": \"postgres\",\n  \"asOfVersion\": 7", postgres);
        Assert.Equal("postgres", SchemaSnapshot.DialectOf(postgres));

        var view = SchemaSnapshot.ExportDdl("v", "view", "create or alter view [v] as select 1 as x", 2, at, "sqlserver").Replace("\r", string.Empty);
        Assert.Contains("\"object\": \"v\",\n  \"dialect\": \"sqlserver\",\n  \"kind\": \"view\"", view);
        Assert.Contains("\"ddl\": \"create view v as select 1 as x\"", view);

        var trigger = SchemaSnapshot.ExportTrigger(
            new TriggerSnapshot("roles", false, new TriggerDefinition("trg", TriggerTiming.After, TriggerEvent.Update, "select 1;")), 13, at, "postgres");
        Assert.Contains("\"dialect\": \"postgres\"", trigger);
    }

    [Fact]
    public void A_snapshot_of_another_dialect_is_MIG_014_before_anything_executes()
    {
        var refused = Assert.Throws<SimpleOrmException>(() => SchemaSnapshot.RequireDialect("postgres", new SqliteDialect(), "V0002 widgets"));
        Assert.Equal("MIG-014", refused.Code);
        Assert.Contains("postgres", refused.Message);
        Assert.Contains("sqlite", refused.Message);

        SchemaSnapshot.RequireDialect("sqlite", new SqliteDialect(), "V0001 widgets");   // unstamped files are SQLite's
        SchemaSnapshot.RequireDialect("SqlServer", new SqlServerDialect(), "V0001 widgets");   // case-insensitive
    }

    [Fact]
    public void The_view_normalizer_strips_every_dialects_prefix_and_quoting()
    {
        const string Neutral = "create view totals as select 1 as answer";
        Assert.Equal(Neutral, SchemaSnapshot.NormalizeDdl("create view if not exists totals as\n  select 1 as answer"));
        Assert.Equal(Neutral, SchemaSnapshot.NormalizeDdl("CREATE VIEW totals as select 1 as answer"));
        Assert.Equal(Neutral, SchemaSnapshot.NormalizeDdl("create or alter view [totals] as\nselect 1 as answer"));
        Assert.Equal(Neutral, SchemaSnapshot.NormalizeDdl("CREATE   VIEW [totals] as select 1 as answer"));   // SQL Server blanks OR ALTER
        Assert.Equal(Neutral, SchemaSnapshot.NormalizeDdl("create or replace view \"totals\" as\nselect 1 as answer"));
        Assert.Equal("create materialized view sales as select 1 as x", SchemaSnapshot.NormalizeDdl("create materialized view if not exists \"sales\" as select 1 as x"));

        Assert.True(SchemaSnapshot.TryParseViewDdl(Neutral, out var name, out var body, out var materialized));
        Assert.Equal(("totals", "select 1 as answer", false), (name, body, materialized));
        Assert.False(SchemaSnapshot.TryParseViewDdl("create table t (x int)", out _, out _, out _));

        Assert.True(SchemaSnapshot.IsDropView("drop view if exists \"totals\"", "totals"));
        Assert.True(SchemaSnapshot.IsDropView("drop materialized view [totals]", "totals"));
        Assert.False(SchemaSnapshot.IsDropView("drop view other", "totals"));
    }

    [Fact]
    public void Every_dialect_renders_the_snapshot_driven_ddl_the_derived_rollback_needs()
    {
        var schema = new TableSchema(
            "widgets",
            [
                new TableSchema.Column("id", "bigint", nullable: false, key: true, generated: true),
                new TableSchema.Column("name", "text", nullable: false),
            ],
            [new TableSchema.Index("ix_widgets_name", [new TableSchema.Index.Part("name", descending: true)], unique: true)]);
        var composite = new TableSchema(
            "links",
            [
                new TableSchema.Column("a", "bigint", nullable: false, key: true),
                new TableSchema.Column("b", "bigint", nullable: false, key: true),
            ],
            []);

        var postgres = new PostgresDialect();
        Assert.Equal(
            "create table if not exists \"widgets\" (\n    \"id\" bigint generated by default as identity primary key,\n    \"name\" text not null\n)",
            postgres.CreateTableSql(schema));
        Assert.Equal(
            "create table if not exists \"links\" (\n    \"a\" bigint not null,\n    \"b\" bigint not null,\n    primary key (\"a\", \"b\")\n)",
            postgres.CreateTableSql(composite));
        Assert.Equal("create unique index if not exists \"ix_widgets_name\" on \"widgets\" (\"name\" desc)", postgres.CreateIndexSql("widgets", schema.Indexes[0]));
        Assert.Equal("drop view if exists \"totals\"", postgres.DropViewSql("totals", materialized: false));
        Assert.Equal("drop materialized view if exists \"totals\"", postgres.DropViewSql("totals", materialized: true));
        Assert.Equal("create or replace view \"totals\" as\nselect 1 as x", postgres.CreateViewSql("totals", "select 1 as x", materialized: false));
        Assert.False(postgres.ViewDefinitionIsVerbatim);
        Assert.Contains("as generated", postgres.ColumnsInfoSql);
        Assert.Equal("postgres", postgres.Name);

        var sqlServer = new SqlServerDialect();
        Assert.Equal(
            "if object_id(N'widgets', N'U') is null\ncreate table [widgets] (\n    [id] bigint identity(1,1) primary key,\n    [name] text not null\n)",
            sqlServer.CreateTableSql(schema));
        Assert.Equal(
            "if object_id(N'links', N'U') is null\ncreate table [links] (\n    [a] bigint not null,\n    [b] bigint not null,\n    primary key ([a], [b])\n)",
            sqlServer.CreateTableSql(composite));
        Assert.Equal(
            "if not exists (select 1 from sys.indexes where name = N'ix_widgets_name' and object_id = object_id(N'widgets'))\ncreate unique index [ix_widgets_name] on [widgets] ([name] desc)",
            sqlServer.CreateIndexSql("widgets", schema.Indexes[0]));
        Assert.Equal("drop view if exists [totals]", sqlServer.DropViewSql("totals", materialized: false));
        Assert.Equal("create or alter view [totals] as\nselect 1 as x", sqlServer.CreateViewSql("totals", "select 1 as x", materialized: false));
        Assert.True(sqlServer.ViewDefinitionIsVerbatim);
        Assert.Contains("as [generated]", sqlServer.ColumnsInfoSql);
        Assert.Equal("sqlserver", sqlServer.Name);

        var sqlite = new SqliteDialect();
        Assert.Equal(SnapshotDdl.CreateTableSql(schema), sqlite.CreateTableSql(schema));   // the frozen reference text
        Assert.Equal("create unique index if not exists ix_widgets_name on widgets (name desc)", sqlite.CreateIndexSql("widgets", schema.Indexes[0]));
        Assert.Equal("drop view if exists totals", sqlite.DropViewSql("totals", materialized: false));
        Assert.Equal("create view if not exists totals as\nselect 1 as x", sqlite.CreateViewSql("totals", "select 1 as x", materialized: false));
        Assert.True(sqlite.ViewDefinitionIsVerbatim);
        Assert.Contains("as generated", sqlite.ColumnsInfoSql);
        Assert.Equal("sqlite", sqlite.Name);
    }

    [Fact]
    public async Task Sqlite_introspection_reports_the_rowid_alias_as_generated_through_the_fifth_column()
    {
        var path = Path.Combine(Path.GetTempPath(), $"simpleorm_tooling_{Guid.NewGuid():N}.db");
        await using var db = await Db.OpenAsync($"Data Source={path}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);
        await db.CreateTableAsync<User>(CancellationToken.None);
        await db.CreateTableAsync<UserRole>(CancellationToken.None);

        var users = await SchemaShadow.IntrospectAsync(db, "users", CancellationToken.None);
        Assert.NotNull(users);
        var id = Assert.Single(users.Columns, c => c.Name == "id");
        Assert.True(id.Key && id.Generated);
        Assert.All(users.Columns.Where(c => c.Name != "id"), c => Assert.False(c.Generated));

        var links = await SchemaShadow.IntrospectAsync(db, "user_roles", CancellationToken.None);
        Assert.NotNull(links);
        Assert.All(links.Columns.Where(c => c.Key), c => Assert.False(c.Generated));   // a composite key is never the rowid alias
        Assert.Null(await SchemaShadow.IntrospectAsync(db, "nowhere", CancellationToken.None));
    }
}
