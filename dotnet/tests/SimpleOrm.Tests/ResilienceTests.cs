using System.Data;
using System.Data.Common;
using System.Diagnostics;
using System.Diagnostics.CodeAnalysis;
using System.Net.Sockets;
using System.Reflection;
using Microsoft.Data.SqlClient;
using Npgsql;
using SimpleOrm.Cli;
using SimpleOrm.Postgres;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Resilience (Level 4, ADR-0047, spec/session.md "Resilience"): the connection-open
/// retry and the command timeout. The SQLite legs run through a decorator around the
/// real dialect — its connection wraps the real SQLite connection, failing the first
/// opens on request and recording the timeout of every command it executes; the
/// PostgreSQL and SQL Server legs let the engine fire the timeout and refuse the open.
/// </summary>
public sealed class ResilienceTests(SqliteFixture fixture) : IClassFixture<SqliteFixture>
{
    private static readonly Command<EmptyArgs> ClearUsers = Query.Inline("delete from users where id < 0");

    // --- the open retry -----------------------------------------------------------------

    [Fact]
    public async Task No_retries_by_default_one_attempt_and_the_exception_as_thrown()
    {
        var failure = new TransientTestException(1);
        var dialect = new FaultyDialect(new SqliteDialect(), failure);
        var thrown = await Assert.ThrowsAsync<TransientTestException>(
            () => Db.OpenAsync(fixture.ConnectionString, new DbOptions { Dialect = dialect }, CancellationToken.None));
        Assert.Same(failure, thrown);
        Assert.Equal(1, dialect.Attempts);
        Assert.Equal(1, dialect.Disposed);
    }

    [Fact]
    public async Task Transient_failures_retry_with_the_fixed_delay_and_then_open()
    {
        var delay = TimeSpan.FromMilliseconds(50);
        var dialect = new FaultyDialect(new SqliteDialect(), new TransientTestException(1), new TransientTestException(2), new TransientTestException(3));
        var stopwatch = Stopwatch.StartNew();
        await using var db = await Db.OpenAsync(
            fixture.ConnectionString, new DbOptions { Dialect = dialect, OpenRetries = 3, OpenRetryDelay = delay }, CancellationToken.None);
        stopwatch.Stop();

        Assert.Equal(4, dialect.Attempts);
        Assert.Equal(3, dialect.Disposed);   // every failed connection, never the session's
        Assert.True(stopwatch.Elapsed >= TimeSpan.FromMilliseconds(3 * 50) - TimeSpan.FromMilliseconds(5), $"elapsed {stopwatch.Elapsed}");
        Query<EmptyArgs, long> one = Query.Inline("select 1");
        Assert.Equal([1L], await db.QueryAsync(one, EmptyArgs.Value, CancellationToken.None));
    }

    [Fact]
    public async Task The_last_failure_surfaces_once_the_retries_are_spent()
    {
        var last = new TransientTestException(3);
        var dialect = new FaultyDialect(new SqliteDialect(), new TransientTestException(1), new TransientTestException(2), last, new TransientTestException(4));
        var thrown = await Assert.ThrowsAsync<TransientTestException>(
            () => Db.OpenAsync(
                fixture.ConnectionString,
                new DbOptions { Dialect = dialect, OpenRetries = 2, OpenRetryDelay = TimeSpan.Zero },
                CancellationToken.None));
        Assert.Same(last, thrown);
        Assert.Equal(3, dialect.Attempts);
    }

    [Fact]
    public async Task A_failure_the_dialect_does_not_call_transient_is_never_retried()
    {
        var failure = new InvalidOperationException("not transient");
        var dialect = new FaultyDialect(new SqliteDialect(), failure);
        var thrown = await Assert.ThrowsAsync<InvalidOperationException>(
            () => Db.OpenAsync(
                fixture.ConnectionString,
                new DbOptions { Dialect = dialect, OpenRetries = 5, OpenRetryDelay = TimeSpan.Zero },
                CancellationToken.None));
        Assert.Same(failure, thrown);
        Assert.Equal(1, dialect.Attempts);
    }

    [Fact]
    public async Task A_cancellation_during_the_delay_ends_the_open_with_no_further_attempt()
    {
        var dialect = new FaultyDialect(new SqliteDialect(), new TransientTestException(1), new TransientTestException(2));
        using var cancel = new CancellationTokenSource(TimeSpan.FromMilliseconds(100));
        await Assert.ThrowsAnyAsync<OperationCanceledException>(
            () => Db.OpenAsync(
                fixture.ConnectionString,
                new DbOptions { Dialect = dialect, OpenRetries = 5, OpenRetryDelay = TimeSpan.FromSeconds(10) },
                cancel.Token));
        Assert.Equal(1, dialect.Attempts);
    }

    [Fact]
    public async Task A_cancellation_thrown_by_the_open_is_never_retried()
    {
        var dialect = new FaultyDialect(new SqliteDialect(), new OperationCanceledException()) { CancellationIsTransient = true };
        await Assert.ThrowsAsync<OperationCanceledException>(
            () => Db.OpenAsync(
                fixture.ConnectionString,
                new DbOptions { Dialect = dialect, OpenRetries = 5, OpenRetryDelay = TimeSpan.Zero },
                CancellationToken.None));
        Assert.Equal(1, dialect.Attempts);
    }

    [Fact]
    public async Task Negative_options_refuse_before_any_attempt()
    {
        var dialect = new FaultyDialect(new SqliteDialect());
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(
            () => Db.OpenAsync(fixture.ConnectionString, new DbOptions { Dialect = dialect, OpenRetries = -1 }, CancellationToken.None));
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(
            () => Db.OpenAsync(fixture.ConnectionString, new DbOptions { Dialect = dialect, OpenRetryDelay = TimeSpan.FromSeconds(-1) }, CancellationToken.None));
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(
            () => Db.OpenAsync(fixture.ConnectionString, new DbOptions { Dialect = dialect, CommandTimeout = TimeSpan.FromSeconds(-1) }, CancellationToken.None));
        Assert.Equal(0, dialect.Attempts);
    }

    // --- the command timeout ------------------------------------------------------------

    [Theory]
    [InlineData(0.0, 1)]
    [InlineData(0.2, 1)]
    [InlineData(1.0, 1)]
    [InlineData(1.5, 2)]
    [InlineData(30.0, 30)]
    public void The_timeout_is_whole_seconds_rounded_up_and_at_least_one(double seconds, int expected)
        => Assert.Equal(expected, Db.TimeoutSeconds(TimeSpan.FromSeconds(seconds)));

    [Fact]
    public void No_timeout_leaves_the_driver_default()
        => Assert.Null(Db.TimeoutSeconds(null));

    [Fact]
    public async Task Unset_every_command_keeps_the_driver_default()
    {
        var driverDefault = new SqliteDialect().CreateConnection(fixture.ConnectionString).CreateCommand().CommandTimeout;
        var dialect = new FaultyDialect(new SqliteDialect());
        await using var db = await Db.OpenAsync(fixture.ConnectionString, new DbOptions { Dialect = dialect }, CancellationToken.None);
        await new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations").MigrateAsync(CancellationToken.None);
        await db.ExecuteAsync(ClearUsers, EmptyArgs.Value, CancellationToken.None);
        Assert.NotEmpty(dialect.Timeouts);
        Assert.All(dialect.Timeouts, t => Assert.Equal(driverDefault, t.Timeout));
    }

    [Fact]
    public async Task Every_path_sends_its_commands_with_the_configured_timeout()
    {
        var dialect = new FaultyDialect(new SqliteDialect());
        var options = new DbOptions { Dialect = dialect, CommandTimeout = TimeSpan.FromSeconds(1.5), Clock = () => TestDb.SeedTime };
        await using var db = await Db.OpenAsync(fixture.ConnectionString, options, CancellationToken.None);

        async Task Path(string name, Func<Task> action)
        {
            var before = dialect.Timeouts.Count;
            await action();
            Assert.True(dialect.Timeouts.Count > before, $"{name} sent no command");
            Assert.All(dialect.Timeouts.Skip(before), t => Assert.True(t.Timeout == 2, $"{name}: '{t.Sql}' carried {t.Timeout}"));
        }

        await Path("migrate", () => new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations").MigrateAsync(CancellationToken.None));
        await Path("validate", () => SchemaGuard.ValidateAsync(db, typeof(User).Assembly, CancellationToken.None));
        await Path("execute", () => db.ExecuteAsync(ClearUsers, EmptyArgs.Value, CancellationToken.None));

        var ada = new User { Name = "ResilientAda", Email = "resilient-ada@example.test", CreatedAtUtc = TestDb.SeedTime };
        await Path("insert", () => db.InsertAsync(ada, CancellationToken.None));
        await Path("query", () => db.QueryAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None));
        await Path("stream", async () =>
        {
            await foreach (var _ in db.StreamAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None))
            {
            }
        });
        await Path("get", () => db.GetAsync<User>(ada.Id, CancellationToken.None));
        await Path("criteria", () => db.Query<User>().Where(Criteria.Eq(nameof(User.Name), "ResilientAda")).ToListAsync(CancellationToken.None));
        await Path("include (join)", () => db.Query<User>().Include(nameof(User.Roles)).Fetch(FetchMode.Join).ToListAsync(CancellationToken.None));
        await Path("include (multi-query)", () => db.Query<User>().Include(nameof(User.Transactions)).ToListAsync(CancellationToken.None));
        await Path("load", () => db.LoadAsync(ada, nameof(User.Roles), CancellationToken.None));
        ada.DisplayName = "Ada";
        await Path("update", () => db.UpdateAsync(ada, CancellationToken.None));
        await Path("update only", () => db.UpdateOnlyAsync(ada, [nameof(User.DisplayName)], CancellationToken.None));
        await Path("batch", async () =>
        {
            await using var batch = db.Batch();
            batch.Execute(ClearUsers, EmptyArgs.Value);
            batch.Execute(ClearUsers, EmptyArgs.Value);
            await batch.SendAsync(CancellationToken.None);
        });
        await Path("delete", () => db.DeleteAsync<User>(ada.Id, CancellationToken.None));

        var tracked = new FaultyDialect(new SqliteDialect());
        await using var session = await Db.OpenAsync(
            fixture.ConnectionString,
            new DbOptions
            {
                Dialect = tracked, Tracking = TrackingMode.Tracked, Batching = BatchingMode.Auto,
                CommandTimeout = TimeSpan.FromSeconds(7), Clock = () => TestDb.SeedTime,
            },
            CancellationToken.None);
        session.Persist(new User { Name = "ResilientGrace", Email = "resilient-grace@example.test", CreatedAtUtc = TestDb.SeedTime });
        session.Persist(new User { Name = "ResilientLinus", Email = "resilient-linus@example.test", CreatedAtUtc = TestDb.SeedTime });
        await session.FlushAsync(CancellationToken.None);
        Assert.Equal(2, tracked.Timeouts.Count);
        Assert.All(tracked.Timeouts, t => Assert.Equal(7, t.Timeout));
    }

    [Fact]
    public void The_cli_reads_command_timeout_in_whole_seconds()
    {
        Assert.Null(CommandTimeoutOption.Parse(null));
        Assert.Equal(TimeSpan.FromSeconds(300), CommandTimeoutOption.Parse("300"));
        foreach (var bad in new[] { "0", "-1", "1.5", "ten", "", " 5" })
        {
            Assert.Equal("CLI", Assert.Throws<SimpleOrmException>(() => CommandTimeoutOption.Parse(bad)).Code);
        }
    }

    // --- the runner's history read swallows nothing ------------------------------------

    private static readonly Func<string, Exception?> FailHistoryRead = sql =>
        sql.StartsWith("select version, object", StringComparison.Ordinal) ? new TestDbException("history read timed out") : null;

    [Fact]
    public async Task A_failing_history_read_fails_migrate_status_and_baseline()
    {
        var dialect = new FaultyDialect(new SqliteDialect());
        await using var db = await Db.OpenAsync(fixture.ConnectionString, new DbOptions { Dialect = dialect }, CancellationToken.None);
        var runner = new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations");
        await runner.MigrateAsync(CancellationToken.None);   // the version table exists from here on

        dialect.FailCommand = FailHistoryRead;
        Assert.Equal("history read timed out", (await Assert.ThrowsAsync<TestDbException>(() => runner.MigrateAsync(CancellationToken.None))).Message);
        await Assert.ThrowsAsync<TestDbException>(() => runner.StatusAsync(CancellationToken.None));
        await Assert.ThrowsAsync<TestDbException>(() => runner.BaselineAsync(1, CancellationToken.None));

        dialect.FailCommand = null;
        Assert.All(await runner.StatusAsync(CancellationToken.None), e => Assert.Equal(MigrationState.Applied, e.State));
    }

    [Fact]
    public async Task Status_on_a_fresh_database_reports_every_entry_pending_and_migrate_still_creates_the_table()
    {
        var path = Path.Combine(Path.GetTempPath(), $"simpleorm_test_{Guid.NewGuid():N}.db");
        try
        {
            var dialect = new FaultyDialect(new SqliteDialect()) { FailCommand = FailHistoryRead };
            await using (var db = await Db.OpenAsync($"Data Source={path}", new DbOptions { Dialect = dialect }, CancellationToken.None))
            {
                var runner = new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations");
                var status = await runner.StatusAsync(CancellationToken.None);   // no version table: introspection says so, no history read
                Assert.NotEmpty(status);
                Assert.All(status, e => Assert.Equal(MigrationState.Pending, e.State));
                Assert.DoesNotContain(dialect.Timeouts, t => t.Sql.StartsWith("select version, object", StringComparison.Ordinal));

                dialect.FailCommand = null;
                Assert.True(await runner.MigrateAsync(CancellationToken.None) > 0);
                Assert.All(await runner.StatusAsync(CancellationToken.None), e => Assert.Equal(MigrationState.Applied, e.State));
            }
        }
        finally
        {
            Microsoft.Data.Sqlite.SqliteConnection.ClearAllPools();
            File.Delete(path);
        }
    }

    // --- the dialects' transient answers ----------------------------------------------

    [Fact]
    public void Sqlite_calls_nothing_transient()
    {
        var dialect = new SqliteDialect();
        Assert.False(dialect.IsTransient(new Microsoft.Data.Sqlite.SqliteException("unable to open database file", 14)));
        Assert.False(dialect.IsTransient(new TimeoutException()));
        Assert.False(dialect.IsTransient(new SocketException((int)SocketError.ConnectionRefused)));
    }

    [Theory]
    [InlineData("08000", true)]
    [InlineData("08001", true)]
    [InlineData("08006", true)]
    [InlineData("08P01", true)]
    [InlineData("53300", true)]
    [InlineData("57P03", true)]
    [InlineData("28P01", false)]   // wrong password
    [InlineData("3D000", false)]   // no such database
    [InlineData("53000", false)]
    [InlineData("40001", false)]   // a statement failure — never asked about
    public void Postgres_calls_the_connection_sqlstates_transient(string sqlState, bool transient)
    {
        var dialect = new PostgresDialect();
        var postgres = new PostgresException("message", "FATAL", "FATAL", sqlState);
        Assert.Equal(transient, dialect.IsTransient(postgres));
        Assert.Equal(transient, dialect.IsTransient(new NpgsqlException("wrapped", postgres)));
        Assert.Equal(transient, PostgresDialect.IsTransientSqlState(sqlState));
    }

    [Fact]
    public void Postgres_and_sql_server_call_socket_io_and_timeout_causes_transient()
    {
        foreach (var dialect in new IDialect[] { new PostgresDialect(), new SqlServerDialect() })
        {
            Assert.True(dialect.IsTransient(new SocketException((int)SocketError.ConnectionRefused)));
            Assert.True(dialect.IsTransient(new NpgsqlException("Failed to connect", new SocketException((int)SocketError.TimedOut))));
            Assert.True(dialect.IsTransient(new InvalidOperationException("outer", new IOException("reset"))));
            Assert.True(dialect.IsTransient(new TimeoutException()));
            Assert.False(dialect.IsTransient(new InvalidOperationException("no cause")));
            Assert.False(dialect.IsTransient(new ArgumentException("bad connection string")));
        }
    }

    [Fact]
    public void Sql_server_pins_its_transient_error_numbers()
    {
        int[] required = [-2, 20, 64, 233, 4060, 4221, 10053, 10054, 10060, 10928, 10929, 40197, 40501, 40613, 49918, 49919, 49920, 11001];
        Assert.All(required, n => Assert.Contains(n, SqlServerDialect.TransientErrorNumbers));
        Assert.Equal(26, SqlServerDialect.TransientErrorNumbers.Count);
        Assert.DoesNotContain(1205, SqlServerDialect.TransientErrorNumbers);   // a deadlock is a statement failure
        Assert.DoesNotContain(18456, SqlServerDialect.TransientErrorNumbers);  // login failed: the same again
    }

    [Theory]
    [InlineData(-2, true)]
    [InlineData(4060, true)]
    [InlineData(40613, true)]
    [InlineData(18456, false)]
    [InlineData(208, false)]
    public void Sql_server_reads_the_number_of_a_real_sql_exception(int number, bool transient)
        => Assert.Equal(transient, new SqlServerDialect().IsTransient(SqlExceptionWith(number)));

    // --- live -----------------------------------------------------------------------------

    [PostgresFact]
    public async Task Postgres_fires_the_timeout_and_surfaces_the_driver_error()
    {
        await using var db = await Db.OpenAsync(
            PostgresFixture.AdminConnectionString,
            new DbOptions { Dialect = new PostgresDialect(), CommandTimeout = TimeSpan.FromSeconds(1), Batching = BatchingMode.Auto },
            CancellationToken.None);
        Command<EmptyArgs> sleep = Query.Inline("select pg_sleep(3)");
        var stopwatch = Stopwatch.StartNew();
        var thrown = await Assert.ThrowsAnyAsync<NpgsqlException>(() => db.ExecuteAsync(sleep, EmptyArgs.Value, CancellationToken.None));
        Assert.True(stopwatch.Elapsed < TimeSpan.FromSeconds(2.9), $"elapsed {stopwatch.Elapsed}");
        Assert.IsType<TimeoutException>(thrown.InnerException);

        // Npgsql cancelled the statement on the server; the session goes on.
        Query<EmptyArgs, int> one = Query.Inline("select 1");
        Assert.Equal([1], await db.QueryAsync(one, EmptyArgs.Value, CancellationToken.None));

#if NET
        using (var batch = db.NewBatch())
        {
            Assert.Equal(1, batch.Timeout);
        }
#endif
    }

    [PostgresFact]
    public async Task Postgres_retries_an_unreachable_server_and_not_a_missing_database()
    {
        var unreachable = new NpgsqlConnectionStringBuilder(PostgresFixture.AdminConnectionString) { Port = 1, Timeout = 2 }.ConnectionString;
        var counting = new CountingOpens(new PostgresDialect());
        await Assert.ThrowsAnyAsync<NpgsqlException>(
            () => Db.OpenAsync(unreachable, new DbOptions { Dialect = counting, OpenRetries = 2, OpenRetryDelay = TimeSpan.FromMilliseconds(50) }, CancellationToken.None));
        Assert.Equal(3, counting.Attempts);

        var absent = new NpgsqlConnectionStringBuilder(PostgresFixture.AdminConnectionString) { Database = "simpleorm_absent_" + Guid.NewGuid().ToString("N") }.ConnectionString;
        counting = new CountingOpens(new PostgresDialect());
        var login = await Assert.ThrowsAsync<PostgresException>(
            () => Db.OpenAsync(absent, new DbOptions { Dialect = counting, OpenRetries = 2, OpenRetryDelay = TimeSpan.FromMilliseconds(50) }, CancellationToken.None));
        Assert.Equal("3D000", login.SqlState);   // no such database: the same again, never retried
        Assert.Equal(1, counting.Attempts);
    }

    [SqlServerFact]
    public async Task Sql_server_fires_the_timeout_and_surfaces_the_driver_error()
    {
        await using var db = await Db.OpenAsync(
            SqlServerFixture.MasterConnectionString,
            new DbOptions { Dialect = new SqlServerDialect(), CommandTimeout = TimeSpan.FromSeconds(1) },
            CancellationToken.None);
        Command<EmptyArgs> wait = Query.Inline("waitfor delay '00:00:03'");
        var thrown = await Assert.ThrowsAsync<SqlException>(() => db.ExecuteAsync(wait, EmptyArgs.Value, CancellationToken.None));
        Assert.Equal(-2, thrown.Number);

        Query<EmptyArgs, int> one = Query.Inline("select 1");
        Assert.Equal([1], await db.QueryAsync(one, EmptyArgs.Value, CancellationToken.None));
    }

    // --- helpers --------------------------------------------------------------------------

    /// <summary>
    /// A SqlException carrying one error number, through the driver's own internal
    /// factory (it has no public constructor) — so the dialect's answer is pinned on
    /// the type it reads, not only on its number list.
    /// </summary>
    private static SqlException SqlExceptionWith(int number)
    {
        var errorCtor = typeof(SqlError).GetConstructors(BindingFlags.NonPublic | BindingFlags.Instance)
            .Where(c => c.GetParameters() is [{ ParameterType: var first }, ..] && first == typeof(int))
            .OrderBy(c => c.GetParameters().Length)
            .First();
        var arguments = errorCtor.GetParameters().Select((p, i) => i == 0 ? number : p.ParameterType switch
        {
            var t when t == typeof(string) => "test",
            var t when t == typeof(byte) => (object)(byte)0,
            var t when t == typeof(int) => 0,
            var t when t == typeof(uint) => 0u,
            _ => null,
        }).ToArray();
        var error = (SqlError)errorCtor.Invoke(arguments);

        var errors = (SqlErrorCollection)Activator.CreateInstance(typeof(SqlErrorCollection), nonPublic: true)!;
        typeof(SqlErrorCollection).GetMethod("Add", BindingFlags.NonPublic | BindingFlags.Instance)!.Invoke(errors, [error]);
        var create = typeof(SqlException).GetMethods(BindingFlags.NonPublic | BindingFlags.Static)
            .Where(m => m.Name == "CreateException" && m.GetParameters() is [{ ParameterType: var a }, { ParameterType: var b }] && a == typeof(SqlErrorCollection) && b == typeof(string))
            .Single();
        var exception = (SqlException)create.Invoke(null, [errors, "16.0"])!;
        Assert.Equal(number, exception.Number);
        return exception;
    }

    private sealed class TransientTestException(int attempt) : Exception($"transient failure {attempt}");

    /// <summary>A driver failure as the runner's old catch-all would have swallowed it.</summary>
    private sealed class TestDbException(string message) : DbException(message);

    /// <summary>Counts the connections the session creates — one per open attempt.</summary>
    private sealed class CountingOpens(IDialect inner) : ForwardingDialect(inner)
    {
        public int Attempts { get; private set; }

        public override DbConnection CreateConnection(string connectionString)
        {
            Attempts++;
            return Inner.CreateConnection(connectionString);
        }
    }

    /// <summary>
    /// The real dialect, decorated: each connection it creates wraps the real one,
    /// throws the next of <c>failures</c> from its open while any remain, and
    /// records the timeout of every command it executes or prepares.
    /// <see cref="TransientTestException"/> is what it calls transient.
    /// </summary>
    private sealed class FaultyDialect(IDialect inner, params Exception[] failures) : ForwardingDialect(inner)
    {
        private readonly Queue<Exception> _failures = new(failures);

        public int Attempts { get; private set; }

        public int Disposed { get; set; }

        public bool CancellationIsTransient { get; init; }

        public List<(string Sql, int Timeout)> Timeouts { get; } = [];

        /// <summary>When set, a command whose text it answers with an exception throws that exception instead of executing.</summary>
        public Func<string, Exception?>? FailCommand { get; set; }

        public override DbConnection CreateConnection(string connectionString)
        {
            Attempts++;
            return new FaultyConnection(Inner.CreateConnection(connectionString), this, _failures.Count > 0 ? _failures.Dequeue() : null);
        }

        public override bool IsTransient(Exception exception)
            => exception is TransientTestException || (CancellationIsTransient && exception is OperationCanceledException);

        // The SQLite run lock opens BEGIN IMMEDIATE on the provider's own connection.
        public override DbTransaction BeginMigrationRunLock(DbConnection connection)
            => Inner.BeginMigrationRunLock(((FaultyConnection)connection).Inner);
    }

    private sealed class FaultyConnection(DbConnection inner, FaultyDialect dialect, Exception? failure) : DbConnection
    {
        public DbConnection Inner => inner;

        [AllowNull]
        public override string ConnectionString
        {
            get => inner.ConnectionString;
            set => inner.ConnectionString = value;
        }

        public override string Database => inner.Database;

        public override string DataSource => inner.DataSource;

        public override string ServerVersion => inner.ServerVersion;

        public override ConnectionState State => inner.State;

        public override void ChangeDatabase(string databaseName) => inner.ChangeDatabase(databaseName);

        public override void Close() => inner.Close();

        public override void Open()
        {
            if (failure is not null)
            {
                throw failure;
            }

            inner.Open();
        }

        public override Task OpenAsync(CancellationToken cancellationToken)
            => failure is not null ? Task.FromException(failure) : inner.OpenAsync(cancellationToken);

        protected override DbTransaction BeginDbTransaction(IsolationLevel isolationLevel) => inner.BeginTransaction(isolationLevel);

        protected override DbCommand CreateDbCommand() => new RecordingCommand(inner.CreateCommand(), this, dialect);

        protected override void Dispose(bool disposing)
        {
            if (disposing)
            {
                if (failure is not null)
                {
                    dialect.Disposed++;
                }

                inner.Dispose();
            }

            base.Dispose(disposing);
        }
    }

    private sealed class RecordingCommand(DbCommand inner, DbConnection connection, FaultyDialect dialect) : DbCommand
    {
        [AllowNull]
        public override string CommandText
        {
            get => inner.CommandText;
            set => inner.CommandText = value;
        }

        public override int CommandTimeout
        {
            get => inner.CommandTimeout;
            set => inner.CommandTimeout = value;
        }

        public override CommandType CommandType
        {
            get => inner.CommandType;
            set => inner.CommandType = value;
        }

        public override bool DesignTimeVisible
        {
            get => inner.DesignTimeVisible;
            set => inner.DesignTimeVisible = value;
        }

        public override UpdateRowSource UpdatedRowSource
        {
            get => inner.UpdatedRowSource;
            set => inner.UpdatedRowSource = value;
        }

        protected override DbConnection? DbConnection
        {
            get => connection;
            set { }
        }

        protected override DbParameterCollection DbParameterCollection => inner.Parameters;

        protected override DbTransaction? DbTransaction
        {
            get => inner.Transaction;
            set => inner.Transaction = value;
        }

        public override void Cancel() => inner.Cancel();

        public override void Prepare()
        {
            Record();
            inner.Prepare();
        }

        protected override DbParameter CreateDbParameter() => inner.CreateParameter();

        public override int ExecuteNonQuery()
        {
            Record();
            return inner.ExecuteNonQuery();
        }

        public override object? ExecuteScalar()
        {
            Record();
            return inner.ExecuteScalar();
        }

        protected override DbDataReader ExecuteDbDataReader(CommandBehavior behavior)
        {
            Record();
            return inner.ExecuteReader(behavior);
        }

        private void Record()
        {
            dialect.Timeouts.Add((inner.CommandText, inner.CommandTimeout));
            if (dialect.FailCommand?.Invoke(inner.CommandText) is { } failure)
            {
                throw failure;
            }
        }

        protected override void Dispose(bool disposing)
        {
            if (disposing)
            {
                inner.Dispose();
            }

            base.Dispose(disposing);
        }
    }
}
