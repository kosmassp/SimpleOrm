using BenchmarkDotNet.Attributes;
using BenchmarkDotNet.Running;
using Dapper;
using Microsoft.Data.Sqlite;
using SimpleOrm;
using SimpleOrm.Sqlite;

BenchmarkRunner.Run<QueryBenchmarks>();

[Table("bench_rows")]
public sealed class BenchRow
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

public sealed record ByIdArgs(long Id);

/// <summary>
/// Milestone 8 (§8.8): SimpleOrm vs Dapper vs raw Microsoft.Data.Sqlite reader
/// code, same schema, same data, one open connection each.
/// Target: within 10% of Dapper on net10.0.
/// </summary>
[MemoryDiagnoser]
[ShortRunJob]
public class QueryBenchmarks
{
    private const string SelectAll = "select id, name, email, age from bench_rows order by id";
    private const string SelectOne = "select id, name, email, age from bench_rows where id = @Id";

    private static readonly Query<EmptyArgs, BenchRow> AllRows = Query.Inline(SelectAll);
    private static readonly Query<ByIdArgs, BenchRow> RowById = Query.Inline(SelectOne);

    private string _path = string.Empty;
    private Db _db = null!;
    private SqliteConnection _dapper = null!;
    private SqliteConnection _raw = null!;

    [GlobalSetup]
    public void Setup()
    {
        _path = Path.Combine(Path.GetTempPath(), $"simpleorm_bench_{Guid.NewGuid():N}.db");
        var connectionString = $"Data Source={_path}";

        using (var seed = new SqliteConnection(connectionString))
        {
            seed.Open();
            seed.Execute(
                "create table bench_rows (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL, age INTEGER NOT NULL) STRICT");
            using var tx = seed.BeginTransaction();
            for (var i = 0; i < 1000; i++)
            {
                seed.Execute(
                    "insert into bench_rows (name, email, age) values (@n, @e, @a)",
                    new { n = "user" + i, e = $"user{i}@example.com", a = (long)(20 + i % 50) }, tx);
            }

            tx.Commit();
        }

        _db = Db.OpenAsync(connectionString, new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None)
            .GetAwaiter().GetResult();
        _dapper = new SqliteConnection(connectionString);
        _dapper.Open();
        _raw = new SqliteConnection(connectionString);
        _raw.Open();
    }

    [GlobalCleanup]
    public void Cleanup()
    {
        _db.DisposeAsync().AsTask().GetAwaiter().GetResult();
        _dapper.Dispose();
        _raw.Dispose();
        SqliteConnection.ClearAllPools();
        File.Delete(_path);
    }

    // --- 1000 rows -----------------------------------------------------------------

    [Benchmark(Baseline = true)]
    public int Dapper_Query1000()
        => _dapper.Query<BenchRow>(SelectAll).AsList().Count;

    [Benchmark]
    public async Task<int> SimpleOrm_Query1000()
        => (await _db.QueryAsync(AllRows, EmptyArgs.Value, CancellationToken.None)).Count;

    [Benchmark]
    public int Raw_Query1000()
    {
        using var command = _raw.CreateCommand();
        command.CommandText = SelectAll;
        using var reader = command.ExecuteReader();
        var rows = new List<BenchRow>();
        while (reader.Read())
        {
            rows.Add(new BenchRow
            {
                Id = reader.GetInt64(0),
                Name = reader.GetString(1),
                Email = reader.GetString(2),
                Age = reader.GetInt64(3),
            });
        }

        return rows.Count;
    }

    // --- single row by key ---------------------------------------------------------

    [Benchmark]
    public BenchRow Dapper_SingleById()
        => _dapper.QueryFirst<BenchRow>(SelectOne, new { Id = 500L });

    [Benchmark]
    public Task<BenchRow> SimpleOrm_GetAsync()
        => _db.GetAsync<BenchRow>(500L, CancellationToken.None);

    [Benchmark]
    public Task<BenchRow> SimpleOrm_QuerySingle()
        => _db.QuerySingleAsync(RowById, new ByIdArgs(500L), CancellationToken.None);
}
