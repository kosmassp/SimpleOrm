using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The statement log (Level 4 item 4, ADR-0045, spec/session.md "Statement log"):
/// one line per statement as it is sent, through <see cref="DbOptions.Log"/>;
/// every execution path writes exactly one, named; parameter values stay out
/// unless asked; the built-in sink writes to standard output.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class StatementLogTests(SqliteFixture fixture)
{
    /// <summary>Entries SchemaGuard never enumerates here: their lines carry the kind of entry.</summary>
    public static class PlainQueries
    {
        public static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users");

        public static readonly Command<NameArgs> RenameAll = Query.Inline("update users set display_name = @Name");
    }

    /// <summary>Entries SchemaGuard enumerates in one test: stamped with their declared names from then on.</summary>
    public static class LogQueries
    {
        public static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users -- notnull: count(id)");

        public static readonly Command<NameArgs> RenameAll = Query.Inline("update users set display_name = @Name");
    }

    public sealed record NameArgs(string Name);

    private async Task<(Db Db, List<string> Lines)> OpenAsync(
        TrackingMode tracking = TrackingMode.Untracked, BatchingMode batching = BatchingMode.Off, bool values = false)
    {
        await using (await TestDb.OpenAsync(fixture))
        {
        }

        var lines = new List<string>();
        var db = await Db.OpenAsync(
            fixture.ConnectionString,
            new DbOptions
            {
                Dialect = new SqliteDialect(), Tracking = tracking, Batching = batching, Log = lines.Add, LogParameterValues = values,
            },
            CancellationToken.None);
        return (db, lines);
    }

    private static IEnumerable<string> NamesOf(IEnumerable<string> lines) => lines.Select(l => l.Substring(0, l.IndexOf(": ", StringComparison.Ordinal)));

    [Fact]
    public async Task Every_execution_path_writes_exactly_one_named_line()
    {
        var (db, lines) = await OpenAsync();
        await using (db)
        {
            var ada = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = TestDb.SeedTime };
            await db.InsertAsync(ada, CancellationToken.None);                                         // User.insert
            ada.Name = "Ada Lovelace";
            await db.UpdateAsync(ada, CancellationToken.None);                                         // User.update
            await db.UpdateOnlyAsync(ada, [nameof(User.DisplayName)], CancellationToken.None);         // User.updateOnly
            await db.GetAsync<User>(ada.Id, CancellationToken.None);                                   // User.get
            await db.Query<User>().Where(Criteria.Eq(nameof(User.Id), ada.Id)).ToListAsync(CancellationToken.None);   // User.criteria
            await db.QueryAllAsync<User>(CancellationToken.None);                                      // User.selectAll
            await db.QueryAsync(PlainQueries.CountUsers, EmptyArgs.Value, CancellationToken.None);      // query (unnamed until SchemaGuard ran)
            await db.QuerySingleAsync(PlainQueries.CountUsers, EmptyArgs.Value, CancellationToken.None); // query
            await foreach (var _ in db.StreamAsync(PlainQueries.CountUsers, EmptyArgs.Value, CancellationToken.None))   // query
            {
            }

            await db.ExecuteAsync(PlainQueries.RenameAll, new NameArgs("x"), CancellationToken.None);   // command
            await db.QueryAsync<DailySales>(new DailySalesArgs(TestDb.SeedTime.AddDays(-1)), CancellationToken.None);   // DailySales.statement

            var role = new Role { Name = "admin", CreatedAtUtc = TestDb.SeedTime };
            await db.InsertAsync(role, CancellationToken.None);                                        // Role.insert
            await db.InsertAsync(new UserRole { UserId = ada.Id, RoleId = role.Id, CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);   // UserRole.insert
            await db.LoadAsync(ada, nameof(User.Roles), CancellationToken.None);                       // UserRole.criteria, Role.criteria (link, then targets)
            await db.Query<User>().Include(nameof(User.Profile)).Fetch(FetchMode.Join).ToListAsync(CancellationToken.None);   // User.include
            await db.DeleteAsync<Role>(role.Id, CancellationToken.None);                               // Role.delete — the soft delete is an update
            await db.DeleteAsync<UserRole>((ada.Id, role.Id), CancellationToken.None);                 // UserRole.delete
            await db.CreateTableAsync<RoleChange>(CancellationToken.None);                             // RoleChange.createTable (if not exists)
        }

        Assert.Equal(
            [
                "User.insert", "User.update", "User.updateOnly", "User.get", "User.criteria", "User.selectAll",
                "query", "query", "query", "command", "DailySales.statement",
                "Role.insert", "UserRole.insert", "UserRole.criteria", "Role.criteria", "User.include",
                "Role.delete", "UserRole.delete", "RoleChange.createTable",
            ],
            NamesOf(lines));

        Assert.StartsWith("User.get: select ", lines[3], StringComparison.Ordinal);
        Assert.EndsWith(" [@k0]", lines[3], StringComparison.Ordinal);                                 // parameter names, in bind order
        Assert.Equal("query: select count(id) from users", lines[6]);                                 // no parameters: no bracket
        Assert.StartsWith("Role.delete: update ", lines[16], StringComparison.Ordinal);               // [SoftDelete]: the flag-setting update
        Assert.DoesNotContain("ada@example.com", lines[0], StringComparison.Ordinal);                // values stay out by default
    }

    [Theory]
    [InlineData(BatchingMode.Off)]
    [InlineData(BatchingMode.Auto)]
    public async Task A_flush_and_a_batch_log_every_statement_in_order(BatchingMode batching)
    {
        var (db, lines) = await OpenAsync(TrackingMode.Tracked, batching);
        await using (db)
        {
            var ada = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = TestDb.SeedTime };
            var grace = new User { Name = "Grace", Email = "grace@example.com", CreatedAtUtc = TestDb.SeedTime };
            db.Persist(ada);
            db.Persist(grace);
            await db.FlushAsync(CancellationToken.None);
            Assert.Equal(["User.insert", "User.insert"], NamesOf(lines));

            lines.Clear();
            await using var batch = db.Batch();
            batch.Insert(new User { Name = "Linus", Email = "linus@example.com", CreatedAtUtc = TestDb.SeedTime });
            batch.Execute(PlainQueries.RenameAll, new NameArgs("y"));
            batch.Delete<User>(ada.Id);
            await batch.SendAsync(CancellationToken.None);
            Assert.Equal(["User.insert", "command", "User.delete"], NamesOf(lines));
        }
    }

    [Fact]
    public async Task Parameter_values_appear_only_when_asked()
    {
        var (db, lines) = await OpenAsync(values: true);
        await using (db)
        {
            var ada = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = TestDb.SeedTime, DisplayName = null };
            await db.InsertAsync(ada, CancellationToken.None);
            await db.GetAsync<User>(ada.Id, CancellationToken.None);
        }

        Assert.Contains("= 'ada@example.com'", lines[0], StringComparison.Ordinal);
        Assert.Contains("= null", lines[0], StringComparison.Ordinal);                                // a NULL binds as null
        Assert.Contains("2026-08-28T09:30:00", lines[0], StringComparison.Ordinal);                  // an instant as the dialect binds it (ISO-8601 text on SQLite)
        Assert.EndsWith($" [@k0 = {ada_id(lines[1])}]", lines[1], StringComparison.Ordinal);

        static string ada_id(string line) => line.Substring(line.LastIndexOf("= ", StringComparison.Ordinal) + 2).TrimEnd(']');
    }

    [Fact]
    public async Task SchemaGuard_logs_its_describe_calls_and_names_the_registry_entries()
    {
        var (db, lines) = await OpenAsync();
        await using (db)
        {
            await SchemaGuard.ValidateTypesAsync(db, [typeof(LogQueries)], CancellationToken.None);
            Assert.NotEmpty(lines);
            Assert.All(lines, line => Assert.StartsWith("validate LogQueries.", line, StringComparison.Ordinal));

            lines.Clear();
            await db.QueryAsync(LogQueries.CountUsers, EmptyArgs.Value, CancellationToken.None);
            await db.ExecuteAsync(LogQueries.RenameAll, new NameArgs("z"), CancellationToken.None);
        }

        Assert.Equal("LogQueries.CountUsers: select count(id) from users -- notnull: count(id)", lines[0]);
        Assert.Equal("LogQueries.RenameAll: update users set display_name = @Name [@Name]", lines[1]);
    }

    [Fact]
    public async Task Migrations_log_under_the_migrate_name()
    {
        var lines = new List<string>();
        await using var db = await Db.OpenAsync(
            fixture.ConnectionString, new DbOptions { Dialect = new SqliteDialect(), Log = lines.Add }, CancellationToken.None);
        await new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations").MigrateAsync(CancellationToken.None);

        Assert.NotEmpty(lines);
        Assert.All(lines, line => Assert.StartsWith("migrate: ", line, StringComparison.Ordinal));
        Assert.Contains(lines, line => line.StartsWith("migrate: select version, object, description, checksum from schema_version", StringComparison.Ordinal));
    }

    [Fact]
    public async Task Nothing_is_formatted_when_the_log_is_unset()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        Assert.Null(db.Options.Log);
        await db.QueryAsync(PlainQueries.CountUsers, EmptyArgs.Value, CancellationToken.None);   // no sink, no line, no error
    }

    [Fact]
    public async Task The_console_sink_writes_each_line_to_standard_output()
    {
        var original = Console.Out;
        var captured = new StringWriter();
        Console.SetOut(captured);
        try
        {
            await using var db = await Db.OpenAsync(
                fixture.ConnectionString, new DbOptions { Dialect = new SqliteDialect(), Log = DbLog.Console }, CancellationToken.None);
            await db.QueryAsync(PlainQueries.CountUsers, EmptyArgs.Value, CancellationToken.None);
        }
        finally
        {
            Console.SetOut(original);
        }

        Assert.Contains("select count(id) from users", captured.ToString(), StringComparison.Ordinal);
    }
}
