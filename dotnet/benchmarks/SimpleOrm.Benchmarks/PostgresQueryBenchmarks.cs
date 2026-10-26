using BenchmarkDotNet.Attributes;
using Dapper;
using Npgsql;
using SimpleOrm;
using SimpleOrm.Postgres;

/// <summary>
/// The <see cref="QueryBenchmarks"/> and <see cref="LoadingBenchmarks"/> shapes on
/// PostgreSQL (the local server, <c>SIMPLEORM_POSTGRES</c> or localhost:5432): mapping
/// 1000 rows, one row by key, a criteria page, and a one-to-many graph (1000 rows ×
/// 3 orders) — SimpleOrm against Dapper and a raw Npgsql reader, same schema, same
/// data, one open connection each. Loopback, so the wire cost is the smallest it
/// can be; what remains is the library's own work plus the driver's.
/// </summary>
[MemoryDiagnoser]
[ShortRunJob]
public class PostgresQueryBenchmarks
{
    private const string SelectAll = "select id, name, email, age from bench_rows order by id";
    private const string SelectOne = "select id, name, email, age from bench_rows where id = @Id";
    private const string SelectByAge = "select id, name, email, age from bench_rows where age = @Age order by id limit 100";
    private const string SelectOrdersByRows = "select id, row_id, amount from bench_orders where row_id = any(@Ids) order by id";

    private static readonly Query<EmptyArgs, BenchRow> AllRows = Query.Inline(SelectAll);
    private static readonly Query<ByIdArgs, BenchRow> RowById = Query.Inline(SelectOne);
    private static readonly Query<ByAgeArgs, BenchRow> RowsByAge = Query.Inline(SelectByAge);

    private static readonly string PostgresAdmin =
        Environment.GetEnvironmentVariable("SIMPLEORM_POSTGRES")
        ?? "Host=localhost;Port=5432;Username=postgres;Password=postgres;Database=postgres;Timeout=5";

    private string _database = string.Empty;
    private string _connectionString = string.Empty;
    private Db _db = null!;
    private NpgsqlConnection _dapper = null!;
    private NpgsqlConnection _raw = null!;

    [GlobalSetup]
    public void Setup()
    {
        _database = $"simpleorm_bench_{Guid.NewGuid():N}";
        using (var admin = new NpgsqlConnection(PostgresAdmin))
        {
            admin.Open();
            admin.Execute($"create database \"{_database}\"");
        }

        _connectionString = new NpgsqlConnectionStringBuilder(PostgresAdmin) { Database = _database }.ConnectionString;
        _db = Db.OpenAsync(_connectionString, new DbOptions { Dialect = new PostgresDialect() }, CancellationToken.None)
            .GetAwaiter().GetResult();
        _db.CreateTableAsync<BenchRow>(CancellationToken.None).GetAwaiter().GetResult();
        _db.CreateTableAsync<BenchOrder>(CancellationToken.None).GetAwaiter().GetResult();

        using (var seed = new NpgsqlConnection(_connectionString))
        {
            seed.Open();
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

        _dapper = new NpgsqlConnection(_connectionString);
        _dapper.Open();
        _raw = new NpgsqlConnection(_connectionString);
        _raw.Open();
    }

    [GlobalCleanup]
    public void Cleanup()
    {
        _db.DisposeAsync().AsTask().GetAwaiter().GetResult();
        _dapper.Dispose();
        _raw.Dispose();
        NpgsqlConnection.ClearAllPools();
        using var admin = new NpgsqlConnection(PostgresAdmin);
        admin.Open();
        admin.Execute($"drop database \"{_database}\" with (force)");
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

    // --- criteria page -------------------------------------------------------------

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

    // --- one-to-many graph: 1000 rows + 3000 orders ----------------------------------

    [Benchmark]
    public int Dapper_TwoQueries_Grouped()
    {
        var rows = _dapper.Query<BenchRow>(SelectAll).AsList();
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
    public Task<int> SimpleOrm_Include_MultiQuery() => IncludeAsync(FetchMode.MultiQuery);

    [Benchmark]
    public Task<int> SimpleOrm_Include_SubSelect() => IncludeAsync(FetchMode.SubSelect);

    [Benchmark]
    public Task<int> SimpleOrm_Include_Join() => IncludeAsync(FetchMode.Join);

    private async Task<int> IncludeAsync(FetchMode mode)
    {
        var rows = await _db.Query<BenchRow>()
            .Include(nameof(BenchRow.Orders)).Fetch(mode)
            .OrderBy(nameof(BenchRow.Id))
            .ToListAsync(CancellationToken.None);
        return rows.Sum(r => r.Orders.Count);
    }
}
