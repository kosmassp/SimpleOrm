using BenchmarkDotNet.Attributes;
using Dapper;
using Microsoft.Data.Sqlite;
using SimpleOrm;
using SimpleOrm.Sqlite;

/// <summary>
/// Level 3 exit (ADR-0034 §7c M6): the tracked session against the untracked one on
/// the same work — the identity map on a 1000-row read (a warm session, where every
/// row is an identity hit, and a cold one that registers and snapshots 1000 rows), the
/// dirty-column update against the full-row one, and a 100-row unit-of-work flush
/// against plain inserts in one transaction and a hand-written Dapper transaction.
/// Writes run in WAL mode with synchronous=OFF so SQLite's journal and fsync do not swamp
/// the library's own cost; the 100-row writers open their session per call so the tracked
/// set stays bounded (a long-lived session's flush sweeps every tracked instance — measured
/// separately by the nothing-to-do flush over 1000 tracked rows).
/// </summary>
[MemoryDiagnoser]
[ShortRunJob]
public class TrackingBenchmarks
{
    private const string SelectAll = "select id, name, email, age from bench_rows order by id";
    private const string SelectOne = "select id, name, email, age from bench_rows where id = @Id";
    private const string UpdateOne = "update bench_rows set name = @Name, email = @Email, age = @Age where id = @Id";
    private const string InsertOne = "insert into bench_inserts (name, email, age) values (@Name, @Email, @Age)";

    private static readonly Query<EmptyArgs, BenchRow> AllRows = Query.Inline(SelectAll);
    private static readonly Command<EmptyArgs> SynchronousOff = Query.Inline("pragma synchronous = OFF");

    private string _path = string.Empty;
    private string _connectionString = string.Empty;
    private Db _untracked = null!;
    private Db _tracked = null!;
    private SqliteConnection _dapper = null!;

    [GlobalSetup]
    public void Setup()
    {
        _path = Path.Combine(Path.GetTempPath(), $"simpleorm_bench_track_{Guid.NewGuid():N}.db");
        _connectionString = $"Data Source={_path}";

        using (var seed = new SqliteConnection(_connectionString))
        {
            seed.Open();
            seed.Execute("pragma journal_mode = WAL");   // persistent for the file: no journal file create/delete per write, which otherwise dominates
            seed.Execute(
                "create table bench_rows (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL, age INTEGER NOT NULL) STRICT");
            seed.Execute(
                "create table bench_inserts (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL, age INTEGER NOT NULL) STRICT");
            using var tx = seed.BeginTransaction();
            for (var i = 0; i < 1000; i++)
            {
                seed.Execute(
                    "insert into bench_rows (name, email, age) values (@n, @e, @a)",
                    new { n = "user" + i, e = $"user{i}@example.com", a = (long)(20 + i % 50) }, tx);
            }

            tx.Commit();
        }

        _untracked = Open(TrackingMode.Untracked);
        _tracked = Open(TrackingMode.Tracked);
        _dapper = new SqliteConnection(_connectionString);
        _dapper.Open();
        _dapper.Execute("pragma synchronous = OFF");
    }

    private Db Open(TrackingMode mode)
    {
        var db = Db.OpenAsync(_connectionString, new DbOptions { Dialect = new SqliteDialect(), Tracking = mode }, CancellationToken.None)
            .GetAwaiter().GetResult();
        db.ExecuteAsync(SynchronousOff, EmptyArgs.Value, CancellationToken.None).GetAwaiter().GetResult();
        return db;
    }

    [GlobalCleanup]
    public void Cleanup()
    {
        _untracked.DisposeAsync().AsTask().GetAwaiter().GetResult();
        _tracked.DisposeAsync().AsTask().GetAwaiter().GetResult();
        _dapper.Dispose();
        SqliteConnection.ClearAllPools();
        File.Delete(_path);
    }

    // --- 1000 rows: the identity map's cost on a read ----------------------------------

    [Benchmark(Baseline = true)]
    public async Task<int> Untracked_Query1000()
        => (await _untracked.QueryAsync(AllRows, EmptyArgs.Value, CancellationToken.None)).Count;

    /// <summary>Every row already tracked: the identity lookup wins, no new instances.</summary>
    [Benchmark]
    public async Task<int> Tracked_Query1000_Warm()
        => (await _tracked.QueryAsync(AllRows, EmptyArgs.Value, CancellationToken.None)).Count;

    /// <summary>A fresh tracked session each time: 1000 registrations and snapshots. Includes the open.</summary>
    [Benchmark]
    public async Task<int> Tracked_Query1000_Cold()
    {
        await using var db = Open(TrackingMode.Tracked);
        return (await db.QueryAsync(AllRows, EmptyArgs.Value, CancellationToken.None)).Count;
    }

    /// <summary>The cold baseline's open cost, so the two cold numbers subtract.</summary>
    [Benchmark]
    public async Task<int> Untracked_Query1000_Cold()
    {
        await using var db = Open(TrackingMode.Untracked);
        return (await db.QueryAsync(AllRows, EmptyArgs.Value, CancellationToken.None)).Count;
    }

    // --- one row: full-row update vs the dirty-column update ----------------------------

    [Benchmark]
    public int Dapper_GetAndUpdate()
    {
        var row = _dapper.QueryFirst<BenchRow>(SelectOne, new { Id = 500L });
        row.Age++;
        return _dapper.Execute(UpdateOne, row);
    }

    [Benchmark]
    public async Task Untracked_GetAndUpdate()
    {
        var row = await _untracked.GetAsync<BenchRow>(500L, CancellationToken.None);
        row.Age++;
        await _untracked.UpdateAsync(row, CancellationToken.None);   // the full row
    }

    [Benchmark]
    public async Task Tracked_GetAndUpdate_Dirty()
    {
        var row = await _tracked.GetAsync<BenchRow>(500L, CancellationToken.None);   // an identity hit after the first call
        row.Age++;
        await _tracked.UpdateAsync(row, CancellationToken.None);   // only age
    }

    /// <summary>Nothing changed: the snapshot comparison and no statement at all.</summary>
    [Benchmark]
    public async Task Tracked_GetAndUpdate_Clean()
    {
        var row = await _tracked.GetAsync<BenchRow>(501L, CancellationToken.None);
        await _tracked.UpdateAsync(row, CancellationToken.None);
    }

    // --- 100 inserts: the unit of work vs plain inserts in one transaction --------------

    [Benchmark]
    public int Dapper_Insert100_Transaction()
    {
        using var connection = new SqliteConnection(_connectionString);
        connection.Open();
        connection.Execute("pragma synchronous = OFF");
        using var tx = connection.BeginTransaction();
        var n = 0;
        for (var i = 0; i < 100; i++)
        {
            n += connection.Execute(InsertOne, new { Name = "n" + i, Email = $"n{i}@example.com", Age = (long)i }, tx);
        }

        tx.Commit();
        return n;
    }

    [Benchmark]
    public async Task<int> Untracked_Insert100_Transaction()
    {
        await using var db = Open(TrackingMode.Untracked);
        await using var tx = await db.BeginAsync(CancellationToken.None);
        for (var i = 0; i < 100; i++)
        {
            await db.InsertAsync(new BenchInsert { Name = "n" + i, Email = $"n{i}@example.com", Age = i }, CancellationToken.None);
        }

        await tx.CommitAsync(CancellationToken.None);
        return 100;
    }

    [Benchmark]
    public async Task<int> Tracked_Persist100_Flush()
    {
        await using var db = Open(TrackingMode.Tracked);
        for (var i = 0; i < 100; i++)
        {
            db.Persist(new BenchInsert { Name = "n" + i, Email = $"n{i}@example.com", Age = i });
        }

        return (await db.FlushAsync(CancellationToken.None)).Inserted;   // its own transaction
    }

    /// <summary>Nothing scheduled, nothing dirty: the per-flush sweep over 1000 tracked instances.</summary>
    [Benchmark]
    public async Task<int> Tracked_Flush_NothingToDo_1000Tracked()
    {
        await _tracked.QueryAsync(AllRows, EmptyArgs.Value, CancellationToken.None);   // identity hits after the first call
        return (await _tracked.FlushAsync(CancellationToken.None)).Inserted;
    }
}

[Table("bench_inserts")]
public sealed class BenchInsert
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    public string Name { get; set; } = string.Empty;

    [Column]
    public string Email { get; set; } = string.Empty;

    [Column]
    public long Age { get; set; }
}
