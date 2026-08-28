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

    // --- generated DDL and CRUD (ADR-0011) ---------------------------------------

    /// <summary>
    /// Creates the entity's table and declared indexes from its metadata
    /// (idempotent: IF NOT EXISTS). A dev/test utility — versioned migrations
    /// (milestone 5) remain the schema-evolution path. Non-table sources throw
    /// <c>DDL-001</c>.
    /// </summary>
    public async Task CreateTableAsync<TEntity>(CancellationToken ct)
        where TEntity : class
    {
        var map = Maps.Load<TEntity>();
        if (map.Kind != RelationKind.Table)
        {
            throw new SimpleOrmException(
                "DDL-001", typeof(TEntity).Name, $"is {map.Kind}-backed; only tables can be created from metadata");
        }

        await ExecuteRawAsync(Options.Dialect.CreateTableSql(map), ct).ConfigureAwait(false);
        foreach (var indexSql in Options.Dialect.CreateIndexSql(map))
        {
            await ExecuteRawAsync(indexSql, ct).ConfigureAwait(false);
        }
    }

    /// <summary>
    /// Creates a view (or materialized view, where the dialect supports them) from
    /// the entity's defining SQL (ADR-0008 addendum 3). Other sources throw
    /// <c>DDL-001</c>; a materialized view on a dialect without them throws <c>DDL-002</c>.
    /// </summary>
    public async Task CreateViewAsync<TEntity>(CancellationToken ct)
        where TEntity : class
    {
        var map = Maps.Load<TEntity>();
        if (map.Kind is not (RelationKind.View or RelationKind.MaterializedView))
        {
            throw new SimpleOrmException(
                "DDL-001", typeof(TEntity).Name, $"is {map.Kind}-backed; CreateViewAsync applies to views only");
        }

        if (map.Kind == RelationKind.MaterializedView && !Options.Dialect.SupportsMaterializedViews)
        {
            throw new SimpleOrmException(
                "DDL-002", typeof(TEntity).Name, "the dialect has no materialized views (SQLite; Level 4 Postgres will)");
        }

        await ExecuteRawAsync(Options.Dialect.CreateViewSql(map), ct).ConfigureAwait(false);
        foreach (var indexSql in Options.Dialect.CreateIndexSql(map))
        {
            await ExecuteRawAsync(indexSql, ct).ConfigureAwait(false);
        }
    }

    /// <summary>
    /// Generated select-all (ADR-0011 addendum): explicit column list from the
    /// metadata, ordered by the key when one exists — no per-table query needed.
    /// Works for tables, views, and materialized views; statements use the typed
    /// statement API and procedures are Level 4 (<c>QRY-005</c>).
    /// </summary>
    public async Task<IReadOnlyList<TEntity>> QueryAllAsync<TEntity>(CancellationToken ct)
        where TEntity : class
    {
        var map = Maps.Load<TEntity>();
        if (map.Kind is RelationKind.Statement or RelationKind.Procedure)
        {
            throw new SimpleOrmException(
                "QRY-005", typeof(TEntity).Name,
                $"is {map.Kind}-backed; select-all needs a named relation (statements execute via the statement API)");
        }

        var sql = "select " + string.Join(", ", map.Properties.Select(p => p.ColumnName))
            + " from " + map.RelationName;
        if (map.KeyProperties.Count > 0)
        {
            sql += " order by " + string.Join(", ", map.KeyProperties.Select(k => k.ColumnName));
        }

        using var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        command.CommandText = sql;
        var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
        try
        {
            var results = new List<TEntity>();
            Func<DbDataReader, TEntity>? plan = null;
            while (await reader.ReadAsync(ct).ConfigureAwait(false))
            {
                plan ??= _mapper.CreatePlan<TEntity>(reader, typeof(TEntity).Name + " select-all");
                results.Add(plan(reader));
            }

            return results;
        }
        finally
        {
            reader.Dispose();
        }
    }

    /// <summary>
    /// Generated insert (§7.14): explicit column list from the metadata, never from
    /// attributes. Writes every non-generated column; a database-generated key is
    /// read back via RETURNING and written onto the entity; an empty client-GUID key
    /// is assigned first. A non-null [ManyToOne] navigation whose key disagrees with
    /// the FK property throws <c>CRUD-004</c> instead of writing; read-only sources
    /// throw <c>CRUD-003</c>.
    /// </summary>
    public async Task InsertAsync<TEntity>(TEntity entity, CancellationToken ct)
        where TEntity : class
    {
        var map = Maps.Load<TEntity>();
        if (map.Kind != RelationKind.Table)
        {
            throw new SimpleOrmException(
                "CRUD-003", typeof(TEntity).Name, $"is {map.Kind}-backed and read-only; writes need a table");
        }

        CheckNavigationConsistency(map, entity);

        if (map.KeyStrategy == KeyStrategy.ClientGuid)
        {
            var keyProperty = map.KeyProperties[0].Property;
            if (Equals(keyProperty.GetValue(entity), Guid.Empty))
            {
                keyProperty.SetValue(entity, Guid.NewGuid());
            }
        }

        using var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        command.CommandText = Options.Dialect.InsertSql(map);
        foreach (var property in map.Properties.Where(p => !p.IsGenerated))
        {
            var parameter = command.CreateParameter();
            parameter.ParameterName = "@" + property.ColumnName;
            parameter.Value = ValueConverter.ToDatabase(property.Property.GetValue(entity));
            command.Parameters.Add(parameter);
        }

        if (map.KeyStrategy == KeyStrategy.DatabaseGenerated)
        {
            var key = map.KeyProperties[0];
            var generated = await command.ExecuteScalarAsync(ct).ConfigureAwait(false);
            key.Property.SetValue(entity, ValueConverter.FromDatabase(
                generated, key.ClrType, $"{typeof(TEntity).Name}.{key.PropertyName}"));
        }
        else
        {
            await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
        }
    }

    private void CheckNavigationConsistency(EntityMap map, object entity)
    {
        foreach (var relationship in map.Relationships)
        {
            var navigation = map.EntityType.GetProperty(relationship.PropertyName)?.GetValue(entity);
            if (navigation is null)
            {
                continue;
            }

            var targetMap = Maps.Load(relationship.TargetType);
            if (targetMap.KeyProperties.Count != 1)
            {
                continue;
            }

            var navigationKey = targetMap.KeyProperties[0].Property.GetValue(navigation);
            var foreignKey = map.Properties
                .First(p => p.PropertyName == relationship.ForeignKeyProperty)
                .Property.GetValue(entity);
            if (!Equals(navigationKey, foreignKey))
            {
                throw new SimpleOrmException(
                    "CRUD-004",
                    $"{map.EntityType.Name}.{relationship.PropertyName}",
                    $"navigation key {navigationKey} disagrees with {relationship.ForeignKeyProperty} = {foreignKey}");
            }
        }
    }

    private async Task ExecuteRawAsync(string sql, CancellationToken ct)
    {
        using var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        command.CommandText = sql;
        await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
    }

    // --- statement-backed entities (ADR-0010): the type IS the query -------------

    /// <summary>Runs a <c>[Statement]</c>-backed entity's own SQL (ADR-0008/0010); args bind against its declared parameters.</summary>
    public async Task<IReadOnlyList<TResult>> QueryAsync<TResult>(object args, CancellationToken ct)
    {
        var results = new List<TResult>();
        await foreach (var row in StreamAsync<TResult>(args, ct).ConfigureAwait(false))
        {
            results.Add(row);
        }

        return results;
    }

    /// <summary>Statement-entity variant of <see cref="QuerySingleAsync{TArgs, TResult}"/> (<c>QRY-001</c>/<c>QRY-002</c>).</summary>
    public async Task<TResult> QuerySingleAsync<TResult>(object args, CancellationToken ct)
    {
        var rows = await QueryAsync<TResult>(args, ct).ConfigureAwait(false);
        return rows.Count switch
        {
            1 => rows[0],
            0 => throw new SimpleOrmException("QRY-001", StatementName<TResult>(), "expected exactly one row, found none"),
            _ => throw new SimpleOrmException("QRY-002", StatementName<TResult>(), $"expected exactly one row, found {rows.Count}"),
        };
    }

    /// <summary>Statement-entity variant of <see cref="QuerySingleOrDefaultAsync{TArgs, TResult}"/>.</summary>
    public async Task<TResult?> QuerySingleOrDefaultAsync<TResult>(object args, CancellationToken ct)
    {
        var rows = await QueryAsync<TResult>(args, ct).ConfigureAwait(false);
        return rows.Count switch
        {
            0 => default,
            1 => rows[0],
            _ => throw new SimpleOrmException("QRY-002", StatementName<TResult>(), $"expected at most one row, found {rows.Count}"),
        };
    }

    /// <summary>Statement-entity variant of <see cref="StreamAsync{TArgs, TResult}"/>.</summary>
    public async IAsyncEnumerable<TResult> StreamAsync<TResult>(
        object args, [EnumeratorCancellation] CancellationToken ct = default)
    {
        var command = CreateStatementCommand<TResult>(args);
        try
        {
            var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
            try
            {
                Func<DbDataReader, TResult>? plan = null;
                while (await reader.ReadAsync(ct).ConfigureAwait(false))
                {
                    plan ??= _mapper.CreatePlan<TResult>(reader, StatementName<TResult>());
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

    private DbCommand CreateStatementCommand<TResult>(object args)
    {
        var map = Maps.Load(typeof(TResult));
        if (map.Kind != RelationKind.Statement)
        {
            throw new SimpleOrmException(
                "QRY-004", typeof(TResult).Name,
                $"is {map.Kind}-backed, not statement-backed; use the registry or generated CRUD for it");
        }

        // The loader already proved declared parameters == SQL placeholders (PRM-010/011);
        // here the args object must match the declaration in type as well as name.
        foreach (var parameter in map.StatementParameters)
        {
            var property = args.GetType().GetProperties()
                .FirstOrDefault(p => string.Equals(p.Name, parameter.Name, StringComparison.OrdinalIgnoreCase));
            if (property is not null)
            {
                var declared = Nullable.GetUnderlyingType(parameter.ClrType) ?? parameter.ClrType;
                var actual = Nullable.GetUnderlyingType(property.PropertyType) ?? property.PropertyType;
                if (declared != actual)
                {
                    throw new SimpleOrmException(
                        "PRM-012", $"{StatementName<TResult>()}.{parameter.Name}",
                        $"declared as {declared.Name}, args supply {actual.Name}");
                }
            }
        }

        var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        ParameterBinder.Bind(command, map.DefiningSql!, args, StatementName<TResult>());
        return command;
    }

    private static string StatementName<TResult>() => typeof(TResult).Name + " [Statement]";

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
