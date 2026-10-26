using BenchmarkDotNet.Attributes;
using Microsoft.Data.Sqlite;
using Npgsql;
using SimpleOrm;
using SimpleOrm.Postgres;
using SimpleOrm.Sqlite;

/// <summary>
/// Level 4 item 3 (ADR-0044): statement batching, <c>Off</c> against <c>Auto</c>,
/// on the same work — a 100-row unit-of-work flush (session opened per call, as in
/// <see cref="TrackingBenchmarks"/>), 100 inserts through one explicit batch, and
/// 100 registry commands through one explicit batch. On SQLite <c>Auto</c> is the
/// sequential realization (one prepared command per statement text; no network
/// round trip exists to save); on PostgreSQL it is <c>NpgsqlBatch</c> — the
/// statements pipelined in messages of 100. PostgreSQL is the local server
/// (<c>SIMPLEORM_POSTGRES</c> or localhost:5432): loopback, so the round trip it
/// saves is the cheapest there is; over a network the gain grows with latency.
/// </summary>
[MemoryDiagnoser]
[ShortRunJob]
public class BatchBenchmarks
{
    private static readonly Command<EmptyArgs> SynchronousOff = Query.Inline("pragma synchronous = OFF");
    private static readonly Command<RenameArgs> Rename = Query.Inline("update bench_rows set name = @Name where id = @Id");

    private static readonly string PostgresAdmin =
        Environment.GetEnvironmentVariable("SIMPLEORM_POSTGRES")
        ?? "Host=localhost;Port=5432;Username=postgres;Password=postgres;Database=postgres;Timeout=5";

    private string _connectionString = string.Empty;
    private string _sqlitePath = string.Empty;
    private string _postgresDatabase = string.Empty;
    private Db _untracked = null!;

    public enum Engine
    {
        Sqlite,
        Postgres,
    }

    [Params(Engine.Sqlite, Engine.Postgres)]
    public Engine Database { get; set; }

    [Params(BatchingMode.Off, BatchingMode.Auto)]
    public BatchingMode Batching { get; set; }

    [GlobalSetup]
    public void Setup()
    {
        if (Database == Engine.Sqlite)
        {
            _sqlitePath = Path.Combine(Path.GetTempPath(), $"simpleorm_bench_batch_{Guid.NewGuid():N}.db");
            _connectionString = $"Data Source={_sqlitePath}";
            using var seed = new SqliteConnection(_connectionString);
            seed.Open();
            using var wal = seed.CreateCommand();
            wal.CommandText = "pragma journal_mode = WAL";
            wal.ExecuteNonQuery();
        }
        else
        {
            _postgresDatabase = $"simpleorm_bench_{Guid.NewGuid():N}";
            using (var admin = new NpgsqlConnection(PostgresAdmin))
            {
                admin.Open();
                using var create = admin.CreateCommand();
                create.CommandText = $"create database \"{_postgresDatabase}\"";
                create.ExecuteNonQuery();
            }

            _connectionString = new NpgsqlConnectionStringBuilder(PostgresAdmin) { Database = _postgresDatabase }.ConnectionString;
        }

        _untracked = Open(TrackingMode.Untracked);
        _untracked.CreateTableAsync<BenchInsert>(CancellationToken.None).GetAwaiter().GetResult();
        _untracked.CreateTableAsync<BenchRow>(CancellationToken.None).GetAwaiter().GetResult();
        _untracked.CreateTableAsync<BenchOrder>(CancellationToken.None).GetAwaiter().GetResult();
        for (var i = 0; i < 100; i++)
        {
            _untracked.InsertAsync(new BenchRow { Name = "row" + i, Email = $"row{i}@example.com", Age = i }, CancellationToken.None)
                .GetAwaiter().GetResult();
        }
    }

    [GlobalCleanup]
    public void Cleanup()
    {
        _untracked.DisposeAsync().AsTask().GetAwaiter().GetResult();
        if (Database == Engine.Sqlite)
        {
            SqliteConnection.ClearAllPools();
            File.Delete(_sqlitePath);
            return;
        }

        NpgsqlConnection.ClearAllPools();
        using var admin = new NpgsqlConnection(PostgresAdmin);
        admin.Open();
        using var drop = admin.CreateCommand();
        drop.CommandText = $"drop database if exists \"{_postgresDatabase}\" with (force)";
        drop.ExecuteNonQuery();
    }

    private Db Open(TrackingMode tracking)
    {
        IDialect dialect = Database == Engine.Sqlite ? new SqliteDialect() : new PostgresDialect();
        var db = Db.OpenAsync(
                _connectionString,
                new DbOptions { Dialect = dialect, Tracking = tracking, Batching = Batching },
                CancellationToken.None)
            .GetAwaiter().GetResult();
        if (Database == Engine.Sqlite)
        {
            db.ExecuteAsync(SynchronousOff, EmptyArgs.Value, CancellationToken.None).GetAwaiter().GetResult();
        }

        return db;
    }

    [Benchmark]
    public async Task<int> Persist100_Flush()
    {
        await using var db = Open(TrackingMode.Tracked);
        for (var i = 0; i < 100; i++)
        {
            db.Persist(new BenchInsert { Name = "n" + i, Email = $"n{i}@example.com", Age = i });
        }

        return (await db.FlushAsync(CancellationToken.None)).Inserted;
    }

    [Benchmark]
    public async Task<int> Batch100_Inserts()
    {
        await using var batch = _untracked.Batch();
        for (var i = 0; i < 100; i++)
        {
            batch.Insert(new BenchInsert { Name = "n" + i, Email = $"n{i}@example.com", Age = i });
        }

        return (await batch.SendAsync(CancellationToken.None)).Affected.Count;
    }

    [Benchmark]
    public async Task<int> Batch100_Commands()
    {
        await using var batch = _untracked.Batch();
        for (var i = 1; i <= 100; i++)
        {
            batch.Execute(Rename, new RenameArgs(i, "renamed" + i));
        }

        return (await batch.SendAsync(CancellationToken.None)).Affected.Sum();
    }
}

public sealed record RenameArgs(long Id, string Name);
