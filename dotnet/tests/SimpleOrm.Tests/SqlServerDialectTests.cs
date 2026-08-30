using SimpleOrm.Sample.Models;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Pure rendering tests for <see cref="SqlServerDialect"/> (ADR-0024) — no
/// database, exactly like the AST conformance runner: the dialect's SQL text is
/// the contract. Live behavior is covered by <see cref="SqlServerIntegrationTests"/>.
/// </summary>
public sealed class SqlServerDialectTests
{
    private static readonly SqlServerDialect Dialect = new();
    private static readonly EntityMapLoader Maps = new();

    private static string Render(SelectAst ast, List<object?> bound)
        => Dialect.SelectSql(ast, (value, _) =>
        {
            bound.Add(value);
            return "@c" + (bound.Count - 1);
        });

    [Fact]
    public void Quotes_brackets_and_escapes_closing_bracket()
    {
        Assert.Equal("[transaction]", Dialect.QuoteIdentifier("transaction"));
        Assert.Equal("[odd]]name]", Dialect.QuoteIdentifier("odd]name"));
    }

    [Fact]
    public void Create_table_renders_identity_key_and_guard()
    {
        var sql = Dialect.CreateTableSql(Maps.Load<User>());
        Assert.StartsWith("if object_id(N'users', N'U') is null\ncreate table [users] (", sql, StringComparison.Ordinal);
        Assert.Contains("[id] bigint identity(1,1) primary key", sql, StringComparison.Ordinal);
        Assert.Contains("[name] nvarchar(max) not null", sql, StringComparison.Ordinal);
        Assert.Contains("[created_at] datetimeoffset not null", sql, StringComparison.Ordinal);
        Assert.Contains("[updated_at] datetimeoffset", sql, StringComparison.Ordinal);
        Assert.DoesNotContain("STRICT", sql, StringComparison.Ordinal);   // types are enforced natively
    }

    [Fact]
    public void Create_table_renders_composite_natural_key()
    {
        var sql = Dialect.CreateTableSql(Maps.Load<UserRole>());
        Assert.Contains("primary key ([user_id], [role_id])", sql, StringComparison.Ordinal);
    }

    [Fact]
    public void Insert_returns_scope_identity_only_for_generated_keys()
    {
        var users = Dialect.InsertSql(Maps.Load<User>());
        Assert.Contains("insert into [users] (", users, StringComparison.Ordinal);
        Assert.EndsWith("; select cast(scope_identity() as bigint)", users, StringComparison.Ordinal);

        var links = Dialect.InsertSql(Maps.Load<UserRole>());
        Assert.DoesNotContain("scope_identity", links, StringComparison.Ordinal);
    }

    [Fact]
    public void Update_bumps_version_and_requires_it()
    {
        var sql = Dialect.UpdateSql(Maps.Load<Transaction>());
        Assert.Contains("[version] = [version] + 1", sql, StringComparison.Ordinal);
        Assert.EndsWith("where [id] = @id and [version] = @version", sql, StringComparison.Ordinal);
    }

    [Fact]
    public void Limit_offset_renders_offset_fetch()
    {
        Assert.Equal("offset @o rows fetch next @l rows only", Dialect.LimitOffsetClause("@l", "@o"));
        Assert.Equal("offset 0 rows fetch next @l rows only", Dialect.LimitOffsetClause("@l", null));
        Assert.Equal("offset @o rows", Dialect.LimitOffsetClause(null, "@o"));
    }

    [Fact]
    public void Unordered_paging_gains_the_placeholder_order_by()
    {
        var bound = new List<object?>();
        var sql = Render(new SelectAst(Maps.Load<User>(), [], [], limit: 2), bound);
        Assert.EndsWith("order by (select null) offset 0 rows fetch next @c0 rows only", sql, StringComparison.Ordinal);
        Assert.Equal([2L], bound);
    }

    [Fact]
    public void Select_quotes_identifiers_and_pages_after_order()
    {
        var bound = new List<object?>();
        var ast = new SelectAst(
            Maps.Load<User>(),
            [Criteria.Ge("CreatedAtUtc", TestDb.SeedTime)],
            [new Ordering("Name", SortOrder.Desc)],
            limit: 20,
            offset: 5);
        var sql = Render(ast, bound);
        Assert.Equal(
            "select [id], [name], [email], [display_name], [created_at], [updated_at] from [users]"
            + " where [created_at] >= @c0 order by [name] desc offset @c2 rows fetch next @c1 rows only",
            sql);
        Assert.Equal(3, bound.Count);   // where value, then limit, then offset — the cross-dialect bind order
    }

    // The composite-membership EXISTS rewrite (SupportsRowValueIn = false) is
    // pinned live by SqlServerIntegrationTests: Criteria.InSelect is internal, and
    // the subselect fetch mode is the only path that produces it.

    [Fact]
    public void Version_table_is_guarded_not_strict()
    {
        Assert.StartsWith("if object_id(N'schema_version', N'U') is null", Dialect.VersionTableSql, StringComparison.Ordinal);
        Assert.DoesNotContain("STRICT", Dialect.VersionTableSql, StringComparison.Ordinal);
    }

    [Fact]
    public void Migration_actions_render_tsql_forms()
    {
        Assert.Equal("exec sp_rename N'roles', N'groups'", Dialect.RenameTableSql("roles", "groups"));
        Assert.Equal("exec sp_rename N'roles.name', N'role_name', 'COLUMN'", Dialect.RenameColumnSql("roles", "name", "role_name"));
        Assert.Equal("alter table [users] add [note] nvarchar(max)", Dialect.AddColumnSql("users", "note", "nvarchar(max)", nullable: true, defaultSql: null));
        Assert.Equal(
            "alter table [users] add [flag] bit not null default 0",
            Dialect.AddColumnSql("users", "flag", "bit", nullable: false, defaultSql: "0"));
        Assert.Equal("alter table [users] drop column [note]", Dialect.DropColumnSql("users", "note"));
        Assert.Equal("drop table [users]", Dialect.DropTableSql("users"));
        Assert.Equal("drop index [ix_users_email] on [users]", Dialect.DropIndexSql("users", "ix_users_email"));
    }

    [Fact]
    public void Storage_types_follow_the_sql_server_conventions()
    {
        var transactions = Maps.Load<Transaction>();
        Assert.Equal("decimal(38, 9)", Dialect.StorageType(transactions.Properties.Single(p => p.PropertyName == "Amount")));
        Assert.Equal("nvarchar(100)", Dialect.StorageType(transactions.Properties.Single(p => p.PropertyName == "Status")));
        Assert.Equal("datetimeoffset", Dialect.StorageType(transactions.Properties.Single(p => p.PropertyName == "CreatedAtUtc")));
        Assert.Equal("bigint", Dialect.StorageType(transactions.Properties.Single(p => p.PropertyName == "Id")));
    }

    [Theory]
    [InlineData("nvarchar(200)", typeof(string), true)]
    [InlineData("nvarchar(max)", typeof(string), true)]
    [InlineData("bit", typeof(bool), true)]
    [InlineData("decimal(18,2)", typeof(decimal), true)]
    [InlineData("datetime2", typeof(DateTime), true)]
    [InlineData("datetimeoffset", typeof(DateTime), true)]
    [InlineData("uniqueidentifier", typeof(Guid), true)]
    [InlineData("int", typeof(long), true)]
    [InlineData("bigint", typeof(int), false)]   // reading bigint into int can overflow — strict
    [InlineData("nvarchar(50)", typeof(int), false)]
    public void Declared_type_compatibility_is_strict(string declared, Type clr, bool compatible)
        => Assert.Equal(compatible, Dialect.IsDeclaredTypeCompatible(declared, clr, enumAsInt: false));
}
