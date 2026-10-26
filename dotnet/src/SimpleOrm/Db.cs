using System.Data.Common;
using System.Runtime.CompilerServices;

namespace SimpleOrm;

/// <summary>
/// The session (§7.17): owns one <see cref="DbConnection"/> obtained from the
/// dialect and, at most, one active transaction. Every command runs on this
/// connection and inside the current transaction, if any. No ambient state.
/// </summary>
public sealed partial class Db : IAsyncDisposable
{
    private readonly DbConnection _connection;
    private readonly ResultMapper _mapper;
    private readonly TypeConverter _converter;
    private DbTransaction? _transaction;

    private Db(DbConnection connection, DbOptions options)
    {
        _connection = connection;
        Options = options;
        Maps = new EntityMapLoader(options.Mapping);
        _converter = new TypeConverter(options.TypeHandlers, options.Dialect.BindsTemporalsNatively);
        _mapper = new ResultMapper(Maps, _converter);
    }

    public DbOptions Options { get; }

    /// <summary>The session's metadata loader (shared cache for this session).</summary>
    public EntityMapLoader Maps { get; }

    internal DbConnection Connection => _connection;

    internal ResultMapper Mapper => _mapper;

    internal TypeConverter Converter => _converter;

    public static async Task<Db> OpenAsync(string connectionString, DbOptions options, CancellationToken ct)
    {
        if (options.MaxStatementsPerMessage < 1)
        {
            throw new ArgumentOutOfRangeException(
                nameof(options), options.MaxStatementsPerMessage, "MaxStatementsPerMessage must be at least 1");
        }

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

    public Task<IReadOnlyList<TResult>> QueryAsync<TArgs, TResult>(
        Query<TArgs, TResult> query, TArgs args, CancellationToken ct)
        => MaterializeAsync<TResult>(CreateCommand(query.Source, args!), query.Source.Description, ct, query.Source.LogName("query"));

    /// <summary>
    /// The one list-materialization loop (milestone 8): the reader opens async, rows
    /// read synchronously — the SQLite provider is synchronous underneath (ADR-0003)
    /// and per-row async machinery only adds overhead — with cooperative
    /// cancellation checked periodically. <paramref name="name"/> names the mapping
    /// plan and its errors; <paramref name="logName"/> the statement log's line.
    /// </summary>
    private async Task<IReadOnlyList<TResult>> MaterializeAsync<TResult>(
        DbCommand command, string name, CancellationToken ct, string logName)
    {
        using (command)
        {
            LogStatement(logName, command);
            var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
            try
            {
                var plan = _mapper.CreatePlan<TResult>(reader, name);
                // Entities read from the database flag their unloaded collection
                // navigations (REL-004 on access, ADR-0021 add.2); loading
                // replaces the sentinel. Null for plain result types.
                var markUnloaded = UnloadedNavigations.MarkerFor(typeof(TResult), Maps);
                var track = TrackerFor(typeof(TResult));
                var results = new List<TResult>();
                var row = 0;
                while (reader.Read())
                {
                    var entity = plan(reader);
                    markUnloaded?.Invoke(entity!);
                    if (track is not null)
                    {
                        entity = (TResult)track(entity!);
                    }

                    results.Add(entity);
                    if ((++row & 63) == 0)
                    {
                        ct.ThrowIfCancellationRequested();
                    }
                }

                return results;
            }
            finally
            {
                reader.Dispose();
            }
        }
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
            LogStatement(query.Source.LogName("query"), command);
            var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
            try
            {
                // Built from the schema before the first row, so strictness
                // (MAP-001/002/003) fires even for empty results.
                var plan = _mapper.CreatePlan<TResult>(reader, query.Source.Description);
                var markUnloaded = UnloadedNavigations.MarkerFor(typeof(TResult), Maps);
                var track = TrackerFor(typeof(TResult));
                while (await reader.ReadAsync(ct).ConfigureAwait(false))
                {
                    var entity = plan(reader);
                    markUnloaded?.Invoke(entity!);
                    if (track is not null)
                    {
                        entity = (TResult)track(entity!);
                    }

                    yield return entity;
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
        LogStatement(command.Source.LogName("command"), dbCommand);
        return await dbCommand.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
    }

    // --- criteria queries and key reads (ADR-0006, ADR-0012) ---------------------

    /// <summary>Starts a criteria query (ADR-0012) over a named readable source; statements/procedures throw <c>QRY-005</c>.</summary>
    public CriteriaQuery<TEntity> Query<TEntity>()
        where TEntity : class
    {
        var map = Maps.Load<TEntity>();
        if (map.Kind is RelationKind.Statement or RelationKind.Procedure)
        {
            throw new SimpleOrmException(
                "QRY-005", typeof(TEntity).Name,
                $"is {map.Kind}-backed; criteria queries need a named relation (statements execute via the statement API)");
        }

        return new CriteriaQuery<TEntity>(this);
    }

    /// <summary>Renders a criteria AST through the dialect and materializes it (§10.4).</summary>
    internal Task<IReadOnlyList<TEntity>> ExecuteAstAsync<TEntity>(SelectAst ast, CancellationToken ct)
        where TEntity : class
    {
        var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        try
        {
            var binder = new CommandParameterBinder(command, _converter, typeof(TEntity).Name + " criteria");
            command.CommandText = Options.Dialect.SelectSql(ast, binder.Add);
        }
        catch
        {
            // Render refusals (QRY-006/007/008) throw after parameters may
            // already sit on the live command — don't leak it.
            command.Dispose();
            throw;
        }

        return MaterializeAsync<TEntity>(command, typeof(TEntity).Name + " criteria", ct, typeof(TEntity).Name + ".criteria");
    }

    /// <summary>Read by key (ADR-0006): a missing row throws <c>CRUD-001</c>; composite keys pass a tuple.</summary>
    public async Task<TEntity> GetAsync<TEntity>(object key, CancellationToken ct)
        where TEntity : class
        => await GetOrDefaultAsync<TEntity>(key, ct).ConfigureAwait(false)
            ?? throw new SimpleOrmException(
                "CRUD-001", typeof(TEntity).Name, $"no row with key ({FormatKey(key)})");

    /// <summary>Read by key (ADR-0006): a missing row returns null.</summary>
    public async Task<TEntity?> GetOrDefaultAsync<TEntity>(object key, CancellationToken ct)
        where TEntity : class
    {
        var map = Maps.Load<TEntity>();
        if (map.Kind is RelationKind.Statement or RelationKind.Procedure)
        {
            throw new SimpleOrmException(
                "QRY-005", typeof(TEntity).Name, $"is {map.Kind}-backed; key reads need a named relation");
        }

        var keyValues = ValidateKey(map, key);
        var fresh = await ReadByKeyAsync<TEntity>(map, keyValues, ct).ConfigureAwait(false);
        return fresh is null ? null : (TEntity)Track(map, fresh);
    }

    /// <summary>One row by key as a fresh instance — the shared read behind <see cref="GetOrDefaultAsync{TEntity}"/> and <see cref="RefreshAsync{TEntity}"/>; never consults the identity map.</summary>
    private async Task<TEntity?> ReadByKeyAsync<TEntity>(EntityMap map, object?[] keyValues, CancellationToken ct)
        where TEntity : class
    {
        using var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        var dialect = Options.Dialect;
        var predicates = new string[keyValues.Length];
        for (var i = 0; i < keyValues.Length; i++)
        {
            var name = "@k" + i;
            predicates[i] = dialect.QuoteIdentifier(map.KeyProperties[i].ColumnName) + " = " + name;
            var parameter = command.CreateParameter();
            parameter.ParameterName = name;
            parameter.Value = _converter.ToDatabase(keyValues[i], $"{typeof(TEntity).Name} key[{i}]");
            command.Parameters.Add(parameter);
        }

        command.CommandText = "select " + string.Join(", ", map.Properties.Select(p => dialect.QuoteIdentifier(p.ColumnName)))
            + " from " + dialect.QuoteIdentifier(map.RelationName!)
            + " where " + string.Join(" and ", predicates);

        LogStatement(typeof(TEntity).Name + ".get", command);
        var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
        try
        {
            var plan = _mapper.CreatePlan<TEntity>(reader, typeof(TEntity).Name + " get");
            var markUnloaded = UnloadedNavigations.MarkerFor(typeof(TEntity), Maps);
            TEntity? result = null;
            while (reader.Read())
            {
                if (result is not null)
                {
                    throw new SimpleOrmException(
                        "QRY-002", typeof(TEntity).Name, "the key matched more than one row");
                }

                result = plan(reader);
                markUnloaded?.Invoke(result);
            }

            return result;
        }
        finally
        {
            reader.Dispose();
        }
    }

    /// <summary>ADR-0006: the key (or tuple) must match the EntityMap key in arity, order, and types.</summary>
    private static object[] ValidateKey(EntityMap map, object key)
    {
        var target = map.EntityType.Name;
        if (map.KeyProperties.Count == 0)
        {
            throw new SimpleOrmException("CRUD-002", target, "the entity defines no key");
        }

        var provided = IsValueTuple(key.GetType())
            ? key.GetType().GetFields().OrderBy(f => f.Name, StringComparer.Ordinal).Select(f => f.GetValue(key)!).ToArray()
            : [key];
        if (provided.Length != map.KeyProperties.Count)
        {
            throw new SimpleOrmException(
                "CRUD-002", target,
                $"the key has {map.KeyProperties.Count} part(s), {provided.Length} value(s) were provided");
        }

        var coerced = new object[provided.Length];
        for (var i = 0; i < provided.Length; i++)
        {
            coerced[i] = CoerceKeyPart(provided[i], map.KeyProperties[i], target, i);
        }

        return coerced;
    }

    private static object CoerceKeyPart(object value, PropertyMap keyProperty, string target, int position)
    {
        var expected = Nullable.GetUnderlyingType(keyProperty.ClrType) ?? keyProperty.ClrType;
        var actual = value.GetType();
        if (actual == expected)
        {
            return value;
        }

        // Safe integer widening so GetAsync<Order>(7) works against a long key.
        if ((expected == typeof(long) || expected == typeof(int))
            && actual is { } t && (t == typeof(int) || t == typeof(short) || t == typeof(byte) || t == typeof(long)))
        {
            try
            {
                return Convert.ChangeType(value, expected, System.Globalization.CultureInfo.InvariantCulture);
            }
            catch (OverflowException)
            {
                // fall through to CRUD-002
            }
        }

        throw new SimpleOrmException(
            "CRUD-002", target,
            $"key part {position} ({keyProperty.PropertyName}) expects {expected.Name}, got {actual.Name}");
    }

    private static bool IsValueTuple(Type type)
        => type.IsGenericType && type.FullName!.StartsWith("System.ValueTuple`", StringComparison.Ordinal);

    private static string FormatKey(object key)
        => IsValueTuple(key.GetType())
            ? string.Join(", ", key.GetType().GetFields().OrderBy(f => f.Name, StringComparer.Ordinal).Select(f => f.GetValue(key)))
            : key.ToString() ?? string.Empty;

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

        await ExecuteRawAsync(typeof(TEntity).Name + ".createTable", Options.Dialect.CreateTableSql(map), ct).ConfigureAwait(false);
        foreach (var indexSql in Options.Dialect.CreateIndexSql(map))
        {
            await ExecuteRawAsync(typeof(TEntity).Name + ".createIndex", indexSql, ct).ConfigureAwait(false);
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

        await ExecuteRawAsync(typeof(TEntity).Name + ".createView", Options.Dialect.CreateViewSql(map), ct).ConfigureAwait(false);
        foreach (var indexSql in Options.Dialect.CreateIndexSql(map))
        {
            await ExecuteRawAsync(typeof(TEntity).Name + ".createIndex", indexSql, ct).ConfigureAwait(false);
        }
    }

    /// <summary>
    /// Creates the entity's declared triggers from its metadata (ADR-0042): each
    /// dropped if present, then created — replace semantics, so the call is
    /// idempotent. A dev/test utility beside <see cref="CreateTableAsync{TEntity}"/>,
    /// and deliberately separate from it: building a schema from metadata never
    /// creates triggers unless asked. Versioned migrations remain the
    /// schema-evolution path. A source that carries no triggers (<c>DDL-001</c>) or
    /// a declaration the dialect has no form for (<c>DDL-005</c>) throws.
    /// </summary>
    public async Task CreateTriggersAsync<TEntity>(CancellationToken ct)
        where TEntity : class
    {
        var map = Maps.Load<TEntity>();
        if (map.Kind is not (RelationKind.Table or RelationKind.View))
        {
            throw new SimpleOrmException(
                "DDL-001", typeof(TEntity).Name, $"is {map.Kind}-backed; triggers belong to tables and views");
        }

        // Rendered before anything runs: a form the dialect lacks (DDL-005) refuses with nothing half-created.
        var statements = map.Triggers
            .SelectMany(trigger => Options.Dialect.DropTriggerSql(map.RelationName!, trigger.Name)
                .Concat(Options.Dialect.CreateTriggerSql(map.RelationName!, map.Kind == RelationKind.View, trigger.Definition)))
            .ToArray();
        foreach (var sql in statements)
        {
            await ExecuteRawAsync(typeof(TEntity).Name + ".createTrigger", sql, ct).ConfigureAwait(false);
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

        var dialect = Options.Dialect;
        var sql = "select " + string.Join(", ", map.Properties.Select(p => dialect.QuoteIdentifier(p.ColumnName)))
            + " from " + dialect.QuoteIdentifier(map.RelationName!);
        if (map.KeyProperties.Count > 0)
        {
            sql += " order by " + string.Join(", ", map.KeyProperties.Select(k => dialect.QuoteIdentifier(k.ColumnName)));
        }

        var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        command.CommandText = sql;
        return await MaterializeAsync<TEntity>(command, typeof(TEntity).Name + " select-all", ct, typeof(TEntity).Name + ".selectAll").ConfigureAwait(false);
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
        => await RunAsync(PrepareInsert(Maps.Load<TEntity>(), entity), ct).ConfigureAwait(false);

    /// <summary>
    /// The insert, prepared (ADR-0044): every check, the hook fill, the client
    /// GUID, the bound statement — nothing executed. Completing it writes the
    /// generated key back and tracks the instance; undoing it puts the instance
    /// back as it was, so a failed write — or a failed batched call — leaves it
    /// untouched.
    /// </summary>
    private PendingWrite PrepareInsert(EntityMap map, object entity)
    {
        if (map.Kind != RelationKind.Table)
        {
            throw new SimpleOrmException(
                "CRUD-003", map.EntityType.Name, $"is {map.Kind}-backed and read-only; writes need a table");
        }

        CheckNavigationConsistency(map, entity);
        CheckIdentity(map, entity);
        var createdBefore = map.CreatedAtProperty?.GetValue(entity);
        var keyBefore = map.GetKeyValues(entity);
        void Restore()
        {
            for (var i = 0; i < keyBefore.Length; i++)
            {
                map.KeyProperties[i].SetValue(entity, keyBefore[i]);
            }

            map.CreatedAtProperty?.SetValue(entity, createdBefore);
        }

        FillCreatedAt(map, entity);
        DbCommand command;
        try
        {
            command = InsertCommand(map, entity);
        }
        catch
        {
            Restore();
            throw;
        }

        var generated = map.KeyStrategy == KeyStrategy.DatabaseGenerated;
        var completed = false;
        return new PendingWrite(command, generated ? WriteResult.Scalar : WriteResult.None, entity, generatesKey: generated)
        {
            Name = map.EntityType.Name + ".insert",
            Key = SetKeyOf(map, entity),
            Complete = result =>
            {
                if (generated)
                {
                    if (result is null or DBNull)
                    {
                        throw new InvalidOperationException($"the insert returned no generated key: {command.CommandText}");
                    }

                    var key = map.KeyProperties[0];
                    key.SetValue(entity, _converter.FromDatabase(
                        result, key.ClrType, $"{map.EntityType.Name}.{key.PropertyName}"));
                }

                completed = true;
                TrackWritten(map, entity);
                return 1;
            },
            Undo = () =>
            {
                if (completed)
                {
                    Untrack(map, map.GetKeyValues(entity));
                }

                Restore();
            },
        };
    }

    /// <summary>The insert statement itself: the generated INSERT, every non-generated column bound; an empty client GUID assigned first.</summary>
    private DbCommand InsertCommand(EntityMap map, object entity)
    {
        if (map.KeyStrategy == KeyStrategy.ClientGuid)
        {
            var keyProperty = map.KeyProperties[0];
            if (Equals(keyProperty.GetValue(entity), Guid.Empty))
            {
                keyProperty.SetValue(entity, Guid.NewGuid());
            }
        }

        var command = _connection.CreateCommand();
        try
        {
            command.CommandText = Options.Dialect.InsertSql(map);
            foreach (var property in map.Properties.Where(p => !p.IsGenerated))
            {
                AddEntityParameter(command, property, entity);
            }

            return command;
        }
        catch
        {
            command.Dispose();
            throw;
        }
    }

    /// <summary>
    /// Generated full-row update by key (§7.15): writes every mapped non-key column.
    /// With a version column (§7.16): sets <c>version = version + 1</c>, requires the
    /// entity's version in the WHERE, throws <see cref="ConcurrencyException"/>
    /// (<c>CRUD-010</c>) on zero rows, and bumps the entity's version on success.
    /// Without one, zero rows is <c>CRUD-001</c>. For a narrower SET see
    /// <see cref="UpdateOnlyAsync{TEntity}"/>.
    /// </summary>
    public async Task UpdateAsync<TEntity>(TEntity entity, CancellationToken ct)
        where TEntity : class
    {
        if (PrepareUpdate(RequireWritableKeyed<TEntity>(), entity) is { } write)
        {
            await RunAsync(write, ct).ConfigureAwait(false);
        }
    }

    /// <summary>The update, prepared; null when a tracked instance is clean — no statement at all.</summary>
    private PendingWrite? PrepareUpdate(EntityMap map, object entity)
    {
        CheckNavigationConsistency(map, entity);

        if (CheckIdentity(map, entity) is { } tracked)
        {
            // A tracked instance writes only what changed since its snapshot
            // (Level 3 M1): the ADR-0028 column-list path, and no statement at
            // all when nothing changed. Key/version/generated columns never
            // appear in a SET — a changed key is UOW-002 above.
            // The [UpdatedAt] stamp is never what makes an instance dirty: a clean
            // instance writes nothing; a dirty one carries the stamp in its SET.
            var dirty = DirtyProperties(map, entity, tracked.Snapshot)
                .Where(p => !p.IsKey && !p.IsVersion && !p.IsGenerated && p.Audit != AuditRole.UpdatedAt)
                .ToList();
            if (dirty.Count == 0)
            {
                return null;
            }

            var set = WithUpdatedAt(map, dirty);
            return PrepareUpdateStatement(
                map, entity, "update", Options.Dialect.UpdateOnlySql(map, set), BoundForUpdate(map, set),
                () => TrackWrittenColumnsUndoably(tracked, map, set));
        }

        // Bind SET values, key values, and the current version for the WHERE;
        // database-generated non-key columns are never written.
        return PrepareUpdateStatement(
            map, entity, "update", Options.Dialect.UpdateSql(map),
            map.Properties.Where(p => p.IsKey || p.IsVersion || !p.IsGenerated),
            () => TrackWrittenUndoably(map, entity));
    }

    /// <summary>The parameters of a column-list update: the SET, the key, and the version when mapped.</summary>
    private static IEnumerable<PropertyMap> BoundForUpdate(EntityMap map, IReadOnlyList<PropertyMap> set)
    {
        var bound = set.Concat(map.KeyProperties);
        return map.VersionProperty is { } version ? bound.Concat(new[] { version }) : bound;
    }

    /// <summary>
    /// Update by column list (ADR-0028): writes only the named properties — the
    /// caller says what changed — with every other rule of
    /// <see cref="UpdateAsync{TEntity}"/> intact: keyed WHERE, version bump and
    /// check (<c>CRUD-010</c>), <c>CRUD-001</c> without a version column. Names are
    /// property names (the criteria vocabulary). An unmapped name is <c>CRUD-005</c>;
    /// a key, version, or generated property is <c>CRUD-006</c>; an empty or
    /// repeating list is <c>CRUD-007</c>. Deliberately not an overload: the ports
    /// have no overloading, and the spec names one operation per concept.
    /// </summary>
    public async Task UpdateOnlyAsync<TEntity>(TEntity entity, IReadOnlyList<string> properties, CancellationToken ct)
        where TEntity : class
        => await RunAsync(PrepareUpdateOnly(RequireWritableKeyed<TEntity>(), entity, properties), ct).ConfigureAwait(false);

    private PendingWrite PrepareUpdateOnly(EntityMap map, object entity, IReadOnlyList<string> properties)
    {
        var set = WithUpdatedAt(map, ResolveUpdateList(map, properties));
        var listed = new HashSet<string>(set.Select(p => p.PropertyName), StringComparer.Ordinal);
        CheckNavigationConsistency(map, entity, listed.Contains);
        var tracked = CheckIdentity(map, entity);

        return PrepareUpdateStatement(
            map, entity, "updateOnly", Options.Dialect.UpdateOnlySql(map, set), BoundForUpdate(map, set),
            () => TrackWrittenColumnsUndoably(tracked, map, set));
    }

    /// <summary>Validates an update-by-column-list (ADR-0028) and resolves it to property maps, in the caller's order.</summary>
    private static IReadOnlyList<PropertyMap> ResolveUpdateList(EntityMap map, IReadOnlyList<string> properties)
    {
        var entityName = map.EntityType.Name;
        if (properties is null || properties.Count == 0)
        {
            throw new SimpleOrmException("CRUD-007", entityName, "update by column list needs at least one property");
        }

        var resolved = new List<PropertyMap>(properties.Count);
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var propertyName in properties)
        {
            var target = $"{entityName}.{propertyName}";
            var property = map.Properties.FirstOrDefault(p => p.PropertyName == propertyName);
            if (property is null && map.OwnedTypes.FirstOrDefault(o => o.PropertyName == propertyName) is { } ownedNavigation)
            {
                // An owned navigation's name stands for all its members (ADR-0030).
                foreach (var member in ownedNavigation.Members)
                {
                    if (!seen.Add(member.PropertyName))
                    {
                        throw new SimpleOrmException("CRUD-007", $"{entityName}.{member.PropertyName}", "is listed more than once");
                    }

                    resolved.Add(member);
                }

                continue;
            }

            if (property is null)
            {
                throw new SimpleOrmException("CRUD-005", target, "is not a mapped property; the list takes property names");
            }

            if (property.IsKey)
            {
                throw new SimpleOrmException("CRUD-006", target, "is a key property; an update never writes the key");
            }

            if (property.IsVersion)
            {
                throw new SimpleOrmException("CRUD-006", target, "is the version column; the database computes it");
            }

            if (property.IsGenerated)
            {
                throw new SimpleOrmException("CRUD-006", target, "is database-generated and never written");
            }

            if (!seen.Add(propertyName))
            {
                throw new SimpleOrmException("CRUD-007", target, "is listed more than once");
            }

            resolved.Add(property);
        }

        return resolved;
    }

    /// <summary>
    /// The shared shape of every generated UPDATE (§7.15–16) — full row, column
    /// list, dirty columns, soft delete: the stamp, the bound statement; completing
    /// it judges the row count, bumps the version, and runs <paramref name="onSuccess"/>
    /// (tracking, forgetting), whose returned action undoes that effect.
    /// </summary>
    private PendingWrite PrepareUpdateStatement(
        EntityMap map, object entity, string operation, string sql, IEnumerable<PropertyMap> bound, Func<Action> onSuccess, Action? restore = null)
    {
        var stampBefore = map.UpdatedAtProperty?.GetValue(entity);
        StampUpdatedAt(map, entity);
        DbCommand command;
        try
        {
            command = _connection.CreateCommand();
            command.CommandText = sql;
            foreach (var property in bound)
            {
                AddEntityParameter(command, property, entity);
            }
        }
        catch
        {
            map.UpdatedAtProperty?.SetValue(entity, stampBefore);   // a failed write leaves the instance as it was
            throw;
        }

        var versionBefore = map.VersionProperty?.GetValue(entity);
        var completed = false;
        Action? undoSuccess = null;
        return new PendingWrite(command, AffectedResult, entity, generatesKey: false)
        {
            Name = map.EntityType.Name + "." + operation,
            Key = SetKeyOf(map, entity),
            Complete = result =>
            {
                var affected = AffectedCount(result, sql);
                if (affected == 0)
                {
                    if (map.VersionProperty is { } version)
                    {
                        throw new ConcurrencyException(
                            map.EntityType.Name,
                            $"update affected no rows: version {version.GetValue(entity)} is stale or the row is gone");
                    }

                    throw new SimpleOrmException(
                        "CRUD-001", map.EntityType.Name, $"update affected no rows: no row with key ({FormatKey(KeyOf(map, entity))})");
                }

                completed = true;
                if (map.VersionProperty is { } bump)
                {
                    var current = Convert.ToInt64(bump.GetValue(entity), System.Globalization.CultureInfo.InvariantCulture);
                    bump.SetValue(entity, Convert.ChangeType(
                        current + 1, bump.ClrType, System.Globalization.CultureInfo.InvariantCulture));
                }

                undoSuccess = onSuccess();
                return affected;
            },
            Undo = () =>
            {
                if (completed)
                {
                    undoSuccess?.Invoke();
                    map.VersionProperty?.SetValue(entity, versionBefore);
                }

                map.UpdatedAtProperty?.SetValue(entity, stampBefore);
                restore?.Invoke();
            },
        };
    }

    /// <summary>
    /// Generated delete (§7.16). Pass a key (or tuple) to delete by key — a missing
    /// row throws <c>CRUD-001</c>. Pass the entity to get the version-checked form
    /// when a version column is mapped: zero rows throws
    /// <see cref="ConcurrencyException"/> (<c>CRUD-010</c>).
    /// </summary>
    public async Task DeleteAsync<TEntity>(object keyOrEntity, CancellationToken ct)
        where TEntity : class
        => await RunAsync(PrepareDelete(RequireWritableKeyed<TEntity>(), keyOrEntity), ct).ConfigureAwait(false);

    private PendingWrite PrepareDelete(EntityMap map, object keyOrEntity)
    {
        var byEntity = map.EntityType.IsInstanceOfType(keyOrEntity);
        if (map.SoftDeleteProperty is not null)
        {
            // A declared soft delete is an update that sets the flag (Level 3 M3).
            return PrepareSoftDelete(map, keyOrEntity, byEntity);
        }

        var checkVersion = byEntity && map.VersionProperty is not null;
        var command = _connection.CreateCommand();
        object?[] key;
        try
        {
            command.CommandText = Options.Dialect.DeleteSql(map, checkVersion);
            if (byEntity)
            {
                CheckIdentity(map, keyOrEntity);
                foreach (var keyProperty in map.KeyProperties)
                {
                    AddEntityParameter(command, keyProperty, keyOrEntity);
                }

                if (checkVersion)
                {
                    AddEntityParameter(command, map.VersionProperty!, keyOrEntity);
                }

                key = map.GetKeyValues(keyOrEntity);
            }
            else
            {
                var keyValues = ValidateKey(map, keyOrEntity);
                for (var i = 0; i < keyValues.Length; i++)
                {
                    var parameter = command.CreateParameter();
                    parameter.ParameterName = "@" + map.KeyProperties[i].ColumnName;
                    parameter.Value = _converter.ToDatabase(keyValues[i], $"{map.EntityType.Name} key[{i}]");
                    command.Parameters.Add(parameter);
                }

                key = keyValues;
            }
        }
        catch
        {
            command.Dispose();
            throw;
        }

        var sql = command.CommandText;
        Action? undoForget = null;
        return new PendingWrite(command, AffectedResult, byEntity ? keyOrEntity : null, generatesKey: false)
        {
            Name = map.EntityType.Name + ".delete",
            Key = new EntityKey(map.EntityType, key),
            Complete = result =>
            {
                var affected = AffectedCount(result, sql);
                if (affected == 0)
                {
                    if (checkVersion)
                    {
                        throw new ConcurrencyException(
                            map.EntityType.Name, "delete affected no rows: the version is stale or the row is gone");
                    }

                    throw new SimpleOrmException(
                        "CRUD-001", map.EntityType.Name,
                        $"delete affected no rows: no row with key ({FormatKey(byEntity ? KeyOf(map, keyOrEntity) : keyOrEntity)})");
                }

                undoForget = Forget(map, key);
                return affected;
            },
            Undo = () => undoForget?.Invoke(),
        };
    }

    private EntityMap RequireWritableKeyed<TEntity>()
        where TEntity : class
    {
        var map = Maps.Load<TEntity>();
        if (map.Kind != RelationKind.Table)
        {
            throw new SimpleOrmException(
                "CRUD-003", typeof(TEntity).Name, $"is {map.Kind}-backed and read-only; writes need a table");
        }

        if (map.KeyProperties.Count == 0)
        {
            throw new SimpleOrmException("CRUD-002", typeof(TEntity).Name, "the entity defines no key");
        }

        return map;
    }

    /// <summary>
    /// How a generated UPDATE or DELETE reports its affected-row count: the
    /// driver's, unless the dialect reports it by a trailing select
    /// (<see cref="IDialect.ReportsAffectedRowsBySelect"/>, ADR-0040 add.3: SQL
    /// Server's <c>select @@rowcount</c>, immune to a trigger's rows) — then the
    /// statement's first scalar is the count. Registry commands keep the driver's.
    /// </summary>
    private WriteResult AffectedResult => Options.Dialect.ReportsAffectedRowsBySelect ? WriteResult.Scalar : WriteResult.Count;

    /// <summary>The count the <c>CRUD-010</c>/<c>CRUD-001</c> rules judge, from a statement's result.</summary>
    private int AffectedCount(object? result, string sql)
    {
        if (!Options.Dialect.ReportsAffectedRowsBySelect)
        {
            return (int)result!;
        }

        // The absence of the scalar is never zero (spec/dialects.md Clarification
        // 24): a batch that yields no count row is a dialect bug, not a conflict.
        if (result is null or DBNull)
        {
            throw new InvalidOperationException(
                $"the dialect reports affected rows by select, but the batch returned no count: {sql}");
        }

        return Convert.ToInt32(result, System.Globalization.CultureInfo.InvariantCulture);
    }

    private void AddEntityParameter(DbCommand command, PropertyMap property, object entity)
    {
        var parameter = command.CreateParameter();
        parameter.ParameterName = "@" + property.ColumnName;
        _converter.BindValue(
            parameter,
            property.GetValue(entity),
            $"{entity.GetType().Name}.{property.PropertyName}",
            property.EnumAsInt,
            property.ClrType);
        command.Parameters.Add(parameter);
    }

    private static object KeyOf(EntityMap map, object entity)
        => map.KeyProperties.Count == 1
            ? map.KeyProperties[0].GetValue(entity)!
            : string.Join(", ", map.GetKeyValues(entity));

    private void CheckNavigationConsistency(EntityMap map, object entity, Func<string, bool>? isWritten = null)
    {
        // Only a many-to-one can disagree with its FK columns; collection
        // navigations carry no FK on this row (ADR-0019). Composite keys check
        // pairwise, FK list against key parts in key order (ADR-0019 add.1). An
        // update by column list (ADR-0028) checks only the FK parts it writes.
        foreach (var relationship in map.Relationships.Where(r => r.Kind == RelationshipKind.ManyToOne))
        {
            var navigation = map.EntityType.GetProperty(relationship.PropertyName)?.GetValue(entity);
            if (navigation is null)
            {
                continue;
            }

            var targetMap = Maps.Load(relationship.TargetType);
            if (targetMap.KeyProperties.Count != relationship.ForeignKeyProperties.Count)
            {
                continue;   // arity problems are loader errors, not write-time ones
            }

            for (var i = 0; i < targetMap.KeyProperties.Count; i++)
            {
                if (isWritten is not null && !isWritten(relationship.ForeignKeyProperties[i]))
                {
                    continue;
                }

                var navigationKey = targetMap.KeyProperties[i].GetValue(navigation);
                var foreignKey = map.Properties
                    .First(p => p.PropertyName == relationship.ForeignKeyProperties[i])
                    .GetValue(entity);
                if (!Equals(navigationKey, foreignKey))
                {
                    throw new SimpleOrmException(
                        "CRUD-004",
                        $"{map.EntityType.Name}.{relationship.PropertyName}",
                        $"navigation key {navigationKey} disagrees with {relationship.ForeignKeyProperties[i]} = {foreignKey}");
                }
            }
        }
    }

    private async Task ExecuteRawAsync(string name, string sql, CancellationToken ct)
    {
        using var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        command.CommandText = sql;
        LogStatement(name, command);
        await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
    }

    // --- statement-backed entities (ADR-0010): the type IS the query -------------

    /// <summary>Runs a <c>[Statement]</c>-backed entity's own SQL (ADR-0008/0010); args bind against its declared parameters.</summary>
    public Task<IReadOnlyList<TResult>> QueryAsync<TResult>(object args, CancellationToken ct)
        => MaterializeAsync<TResult>(CreateStatementCommand<TResult>(args), StatementName<TResult>(), ct, typeof(TResult).Name + ".statement");

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
            LogStatement(typeof(TResult).Name + ".statement", command);
            var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
            try
            {
                var plan = _mapper.CreatePlan<TResult>(reader, StatementName<TResult>());
                var markUnloaded = UnloadedNavigations.MarkerFor(typeof(TResult), Maps);
                var track = TrackerFor(typeof(TResult));
                while (await reader.ReadAsync(ct).ConfigureAwait(false))
                {
                    var entity = plan(reader);
                    markUnloaded?.Invoke(entity!);
                    if (track is not null)
                    {
                        entity = (TResult)track(entity!);
                    }

                    yield return entity;
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
        ParameterBinder.Bind(
            command, map.DefiningSql!, args, StatementName<TResult>(), _converter,
            Options.Dialect.SupportsArrayParameters);
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
        ParameterBinder.Bind(
            command, source.Sql, args, source.Description, _converter,
            Options.Dialect.SupportsArrayParameters);
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
