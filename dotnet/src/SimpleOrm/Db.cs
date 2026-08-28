using System.Data.Common;
using System.Runtime.CompilerServices;

namespace SimpleOrm;

/// <summary>
/// The session (§7.17): owns one <see cref="DbConnection"/> obtained from the
/// dialect and, at most, one active transaction. Every command runs on this
/// connection and inside the current transaction, if any. No ambient state.
/// </summary>
public sealed class Db : IAsyncDisposable
{
    private readonly DbConnection _connection;
    private readonly ResultMapper _mapper;
    private DbTransaction? _transaction;

    private Db(DbConnection connection, DbOptions options)
    {
        _connection = connection;
        Options = options;
        Maps = new EntityMapLoader(options.Mapping);
        _mapper = new ResultMapper(Maps);
    }

    public DbOptions Options { get; }

    /// <summary>The session's metadata loader (shared cache for this session).</summary>
    public EntityMapLoader Maps { get; }

    public static async Task<Db> OpenAsync(string connectionString, DbOptions options, CancellationToken ct)
    {
        var connection = options.Dialect.CreateConnection(connectionString);
        try
        {
            await connection.OpenAsync(ct).ConfigureAwait(false);
        }
        catch
        {
            connection.Dispose();
            throw;
        }

        return new Db(connection, options);
    }

    public async Task<IReadOnlyList<TResult>> QueryAsync<TArgs, TResult>(
        Query<TArgs, TResult> query, TArgs args, CancellationToken ct)
    {
        var results = new List<TResult>();
        await foreach (var row in StreamAsync(query, args, ct).ConfigureAwait(false))
        {
            results.Add(row);
        }

        return results;
    }

    /// <summary>Exactly one row; zero rows throws <c>QRY-001</c>, more than one throws <c>QRY-002</c>.</summary>
    public async Task<TResult> QuerySingleAsync<TArgs, TResult>(
        Query<TArgs, TResult> query, TArgs args, CancellationToken ct)
    {
        var rows = await QueryAsync(query, args, ct).ConfigureAwait(false);
        return rows.Count switch
        {
            1 => rows[0],
            0 => throw new SimpleOrmException("QRY-001", query.Source.Description, "expected exactly one row, found none"),
            _ => throw new SimpleOrmException("QRY-002", query.Source.Description, $"expected exactly one row, found {rows.Count}"),
        };
    }

    /// <summary>At most one row; zero rows returns default, more than one throws <c>QRY-002</c>.</summary>
    public async Task<TResult?> QuerySingleOrDefaultAsync<TArgs, TResult>(
        Query<TArgs, TResult> query, TArgs args, CancellationToken ct)
    {
        var rows = await QueryAsync(query, args, ct).ConfigureAwait(false);
        return rows.Count switch
        {
            0 => default,
            1 => rows[0],
            _ => throw new SimpleOrmException("QRY-002", query.Source.Description, $"expected at most one row, found {rows.Count}"),
        };
    }

    public async IAsyncEnumerable<TResult> StreamAsync<TArgs, TResult>(
        Query<TArgs, TResult> query, TArgs args, [EnumeratorCancellation] CancellationToken ct = default)
    {
        var command = CreateCommand(query.Source, args!);
        try
        {
            var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
            try
            {
                Func<DbDataReader, TResult>? plan = null;
                while (await reader.ReadAsync(ct).ConfigureAwait(false))
                {
                    plan ??= _mapper.CreatePlan<TResult>(reader, query.Source.Description);
                    yield return plan(reader);
                }
            }
            finally
            {
                reader.Dispose();
            }
        }
        finally
        {
            command.Dispose();
        }
    }

    public async Task<int> ExecuteAsync<TArgs>(Command<TArgs> command, TArgs args, CancellationToken ct)
    {
        using var dbCommand = CreateCommand(command.Source, args!);
        return await dbCommand.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
    }

    /// <summary>Begins the session's transaction scope; a second concurrent scope throws <c>TX-001</c>.</summary>
    public Task<DbTransactionScope> BeginAsync(CancellationToken ct)
    {
        if (_transaction is not null)
        {
            throw new SimpleOrmException("TX-001", "session", "a transaction is already active on this session");
        }

        ct.ThrowIfCancellationRequested();
        _transaction = _connection.BeginTransaction();
        return Task.FromResult(new DbTransactionScope(this));
    }

    internal void CommitTransaction()
    {
        _transaction?.Commit();
        ClearTransaction();
    }

    internal void RollbackTransaction()
    {
        _transaction?.Rollback();
        ClearTransaction();
    }

    private void ClearTransaction()
    {
        _transaction?.Dispose();
        _transaction = null;
    }

    private DbCommand CreateCommand(SqlSource source, object args)
    {
        var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        ParameterBinder.Bind(command, source.Sql, args, source.Description);
        return command;
    }

    public ValueTask DisposeAsync()
    {
        if (_transaction is not null)
        {
            RollbackTransaction();
        }

        _connection.Dispose();
        return default;
    }
}

/// <summary>
/// A transaction scope on one session. Commit explicitly; disposing an uncommitted
/// scope rolls back.
/// </summary>
public sealed class DbTransactionScope : IAsyncDisposable
{
    private readonly Db _db;
    private bool _completed;

    internal DbTransactionScope(Db db) => _db = db;

    public Task CommitAsync(CancellationToken ct)
    {
        ct.ThrowIfCancellationRequested();
        _db.CommitTransaction();
        _completed = true;
        return Task.CompletedTask;
    }

    public Task RollbackAsync(CancellationToken ct)
    {
        _db.RollbackTransaction();
        _completed = true;
        return Task.CompletedTask;
    }

    public ValueTask DisposeAsync()
    {
        if (!_completed)
        {
            _db.RollbackTransaction();
            _completed = true;
        }

        return default;
    }
}
