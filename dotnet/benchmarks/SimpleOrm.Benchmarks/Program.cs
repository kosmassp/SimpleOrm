using BenchmarkDotNet.Attributes;
using BenchmarkDotNet.Running;
using Dapper;
using Microsoft.Data.Sqlite;
using SimpleOrm;
using SimpleOrm.Sqlite;

BenchmarkRunner.Run(typeof(QueryBenchmarks).Assembly);

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

    /// <summary>Level 2 loading benchmarks: populated by Include/LoadEach only (REL-004 otherwise).</summary>
    [OneToMany(nameof(BenchOrder.RowId))]
    public IReadOnlyList<BenchOrder> Orders { get; private set; } = [];
}

[Table("bench_orders")]
public sealed class BenchOrder
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    [ForeignKey(typeof(BenchRow))]
    public long RowId { get; set; }

    [Column]
    public long Amount { get; set; }
}

public sealed record ByIdArgs(long Id);
public sealed record ByAgeArgs(long Age);

/// <summary>
/// Level 2 exit (ADR-0029): the criteria path and the three eager-loading modes
/// against what a Dapper user writes by hand for the same graph — two queries
/// plus an in-memory group (the batch idiom) and a multi-mapped join. 1000 rows,
/// 3 orders each. SimpleOrm builds the object graph; the Dapper baselines stop
/// at the grouped lookup, so they carry slightly less work than they appear to.
/// </summary>
[MemoryDiagnoser]
[ShortRunJob]
public class LoadingBenchmarks
{
    private const string SelectRows = "select id, name, email, age from bench_rows order by id";
    private const string SelectOrdersByRows = "select id, row_id, amount from bench_orders where row_id in @Ids order by id";
    private const string SelectByAge = "select id, name, email, age from bench_rows where age = @Age order by id limit 100";
    private const string JoinOrdersRows =
        "select o.id, o.row_id, o.amount, r.id, r.name, r.email, r.age from bench_orders o join bench_rows r on r.id = o.row_id order by o.row_id, o.id";

    private static readonly Query<ByAgeArgs, BenchRow> RowsByAge = Query.Inline(SelectByAge);

    private string _path = string.Empty;
    private Db _db = null!;
    private SqliteConnection _dapper = null!;

    [GlobalSetup]
    public void Setup()
    {
        _path = Path.Combine(Path.GetTempPath(), $"simpleorm_bench_load_{Guid.NewGuid():N}.db");
        var connectionString = $"Data Source={_path}";

        using (var seed = new SqliteConnection(connectionString))
        {
            seed.Open();
            seed.Execute(
                "create table bench_rows (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL, age INTEGER NOT NULL) STRICT");
            seed.Execute(
                "create table bench_orders (id INTEGER PRIMARY KEY, row_id INTEGER NOT NULL, amount INTEGER NOT NULL) STRICT");
            seed.Execute("create index ix_bench_orders_row_id on bench_orders (row_id)");
            using var tx = seed.BeginTransaction();
            for (var i = 0; i < 1000; i++)
            {
                seed.Execute(
                    "insert into bench_rows (name, email, age) values (@n, @e, @a)",
                    new { n = "user" + i, e = $"user{i}@example.com", a = (long)(20 + i % 50) }, tx);
                for (var j = 0; j < 3; j++)
                {
                    seed.Execute(
                        "insert into bench_orders (row_id, amount) values (@r, @m)",
                        new { r = (long)(i + 1), m = (long)(j * 10) }, tx);
                }
            }

            tx.Commit();
        }

        _db = Db.OpenAsync(connectionString, new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None)
            .GetAwaiter().GetResult();
        _dapper = new SqliteConnection(connectionString);
        _dapper.Open();
    }

    [GlobalCleanup]
    public void Cleanup()
    {
        _db.DisposeAsync().AsTask().GetAwaiter().GetResult();
        _dapper.Dispose();
        SqliteConnection.ClearAllPools();
        File.Delete(_path);
    }

    // --- criteria (AST → dialect) vs hand-written SQL ---------------------------------

    [Benchmark]
    public int Dapper_Criteria100()
        => _dapper.Query<BenchRow>(SelectByAge, new { Age = 30L }).AsList().Count;

    [Benchmark]
    public async Task<int> SimpleOrm_Criteria100()
        => (await _db.Query<BenchRow>()
            .Where(Criteria.Eq(nameof(BenchRow.Age), 30L))
            .OrderBy(nameof(BenchRow.Id)).Limit(100)
            .ToListAsync(CancellationToken.None)).Count;

    [Benchmark]
    public async Task<int> SimpleOrm_RegistryQuery100()
        => (await _db.QueryAsync(RowsByAge, new ByAgeArgs(30L), CancellationToken.None)).Count;

    // --- one-to-many graph: 1000 rows + 3000 orders -----------------------------------

    [Benchmark(Baseline = true)]
    public int Dapper_TwoQueries_Grouped()
    {
        var rows = _dapper.Query<BenchRow>(SelectRows).AsList();
        var orders = _dapper.Query<BenchOrder>(SelectOrdersByRows, new { Ids = rows.Select(r => r.Id).ToArray() });
        var byRow = orders.ToLookup(o => o.RowId);
        var total = 0;
        foreach (var row in rows)
        {
            total += byRow[row.Id].Count();
        }

        return total;
    }

    [Benchmark]
    public int Dapper_MultiMapJoin()
    {
        var rows = new Dictionary<long, (BenchRow Row, List<BenchOrder> Orders)>();
        _dapper.Query<BenchOrder, BenchRow, BenchOrder>(
            JoinOrdersRows,
            (order, row) =>
            {
                if (!rows.TryGetValue(row.Id, out var entry))
                {
                    entry = (row, []);
                    rows.Add(row.Id, entry);
                }

                entry.Orders.Add(order);
                return order;
            },
            splitOn: "id");
        return rows.Values.Sum(e => e.Orders.Count);
    }

    [Benchmark]
    public Task<int> SimpleOrm_Include_MultiQuery() => IncludeAsync(FetchMode.MultiQuery);

    [Benchmark]
    public Task<int> SimpleOrm_Include_SubSelect() => IncludeAsync(FetchMode.SubSelect);

    [Benchmark]
    public Task<int> SimpleOrm_Include_Join() => IncludeAsync(FetchMode.Join);

    [Benchmark]
    public async Task<int> SimpleOrm_LoadEach()
    {
        var rows = await _db.Query<BenchRow>().OrderBy(nameof(BenchRow.Id)).ToListAsync(CancellationToken.None);
        await _db.LoadEachAsync(rows, nameof(BenchRow.Orders), CancellationToken.None);
        return rows.Sum(r => r.Orders.Count);
    }

    private async Task<int> IncludeAsync(FetchMode mode)
    {
        var rows = await _db.Query<BenchRow>()
            .Include(nameof(BenchRow.Orders)).Fetch(mode)
            .OrderBy(nameof(BenchRow.Id))
            .ToListAsync(CancellationToken.None);
        return rows.Sum(r => r.Orders.Count);
    }
}

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
