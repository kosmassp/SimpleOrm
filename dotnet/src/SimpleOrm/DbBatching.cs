using System.Data.Common;

namespace SimpleOrm;

/// <summary>What a prepared statement returns that the session reads (ADR-0044).</summary>
internal enum WriteResult
{
    /// <summary>Nothing: an insert whose key is not generated.</summary>
    None,

    /// <summary>One result set, one row, one column: a generated key, or the dialect's reported affected-row count.</summary>
    Scalar,

    /// <summary>The driver's affected-row count for the statement.</summary>
    Count,
}

/// <summary>An instance's identity as the batching plan compares it (rule 1a): the entity type and a key that is set.</summary>
internal readonly record struct EntityKey(Type Type, object?[] Values);

/// <summary>
/// One statement, prepared and not yet executed (ADR-0044): the bound command,
/// the result the session reads, and two continuations — <see cref="Complete"/>
/// judges the result and applies it (keys, versions, tracking) exactly as the
/// immediate operation does, <see cref="Undo"/> puts every in-memory effect of the
/// preparation and the completion back. Every generated write and every batched
/// registry command goes through this one shape, whether it runs alone, in a
/// flush, or in a batch send.
/// </summary>
internal sealed class PendingWrite(DbCommand command, WriteResult result, object? instance, bool generatesKey)
{
    public DbCommand Command { get; } = command;

    public WriteResult Result { get; } = result;

    /// <summary>The instance the statement writes; null for a delete by key and a registry command.</summary>
    public object? Instance { get; } = instance;

    /// <summary>An insert whose key the database generates — the key later statements may read (plan rule 1b).</summary>
    public bool GeneratesKey { get; } = generatesKey;

    /// <summary>A registry command (plan rule 2; its count is the driver's).</summary>
    public bool IsCommand { get; init; }

    /// <summary>The key the statement was prepared with, when set (plan rule 1a); null for an unset key and a registry command.</summary>
    public EntityKey? Key { get; init; }

    /// <summary>The operation's name in the statement log (<c>User.insert</c>, a registry command's name).</summary>
    public required string Name { get; init; }

    /// <summary>Judges and applies the statement's result; returns the count the batch result reports.</summary>
    public required Func<object?, int> Complete { get; init; }

    public required Action Undo { get; init; }

    /// <summary>The count <see cref="Complete"/> returned.</summary>
    public int Affected { get; set; }
}

/// <summary>
/// Statement batching (Level 4, ADR-0044, spec/session.md "Batching"): the plan
/// that groups one call's statements into messages, and the realizations that
/// send a message — one <c>DbBatch</c> where the provider has it
/// (<see cref="BatchingInEffect"/>), else statement by statement reusing one
/// prepared command per text.
/// </summary>
public sealed partial class Db
{
    /// <summary>
    /// Whether a planned message goes out as one round trip (ADR-0044): the option
    /// is <see cref="BatchingMode.Auto"/>, the dialect's driver batches
    /// (<see cref="IDialect.SupportsStatementBatching"/>), and the connection can
    /// create a <c>DbBatch</c>. False under <c>Auto</c> means the plan runs
    /// statement by statement — SQLite, and every dialect on <c>netstandard2.0</c>.
    /// </summary>
    public bool BatchingInEffect
#if NET
        => Options.Batching == BatchingMode.Auto && Options.Dialect.SupportsStatementBatching && _connection.CanCreateBatch;
#else
        => false;
#endif

    /// <summary>Test hook: the size of every message a flush or a batch send formed (spec/session.md "The step scripts"). Never public API.</summary>
    internal Action<int>? MessageObserver { get; set; }

    /// <summary>Opens an explicit batch on this session (spec/session.md "The explicit batch"); queuing does no I/O.</summary>
    public StatementBatch Batch() => new(this);

    /// <summary>One prepared write as its own round trip — every immediate operation (§7.14–16).</summary>
    private async Task<int> RunAsync(PendingWrite write, CancellationToken ct)
    {
        try
        {
            return write.Complete(await ExecuteOneAsync(write, reuse: null, ct).ConfigureAwait(false));
        }
        catch
        {
            write.Undo();
            throw;
        }
        finally
        {
            write.Command.Dispose();
        }
    }

    /// <summary>
    /// Executes one prepared statement and returns its result for
    /// <see cref="PendingWrite.Complete"/>. With <paramref name="reuse"/> (the
    /// sequential realization under <c>Auto</c>), a statement whose text ran
    /// before in the same call runs on that command again with its own
    /// parameters — the provider keeps the prepared statement.
    /// </summary>
    private async Task<object?> ExecuteOneAsync(PendingWrite write, Dictionary<string, DbCommand>? reuse, CancellationToken ct)
    {
        var command = write.Command;
        if (reuse is not null)
        {
            if (reuse.TryGetValue(command.CommandText, out var prepared))
            {
                MoveParameters(command, prepared.Parameters);
                command = prepared;
            }
            else
            {
                reuse[command.CommandText] = command;
            }
        }

        command.Transaction = _transaction;
        LogStatement(write.Name, command);
        switch (write.Result)
        {
            case WriteResult.Scalar:
                return await command.ExecuteScalarAsync(ct).ConfigureAwait(false);
            case WriteResult.Count:
                return await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
            default:
                await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
                return null;
        }
    }

    /// <summary>Moves a prepared statement's parameters to another parameter collection (a reused command, a batch command); a parameter belongs to one collection at a time.</summary>
    private static void MoveParameters(DbCommand from, DbParameterCollection to)
    {
        var parameters = from.Parameters.Cast<DbParameter>().ToArray();   // CopyTo wants the provider's own array type
        from.Parameters.Clear();
        to.Clear();
        foreach (var parameter in parameters)
        {
            to.Add(parameter);
        }
    }

    // --- the explicit batch's preparations: the immediate operations' own, by entity type

    internal PendingWrite PrepareInsertFor(EntityMap map, object entity) => PrepareInsert(map, entity);

    internal PendingWrite? PrepareUpdateFor<TEntity>(TEntity entity)
        where TEntity : class
        => PrepareUpdate(RequireWritableKeyed<TEntity>(), entity);

    internal PendingWrite PrepareUpdateOnlyFor<TEntity>(TEntity entity, IReadOnlyList<string> properties)
        where TEntity : class
        => PrepareUpdateOnly(RequireWritableKeyed<TEntity>(), entity, properties);

    internal PendingWrite PrepareDeleteFor<TEntity>(object keyOrEntity)
        where TEntity : class
        => PrepareDelete(RequireWritableKeyed<TEntity>(), keyOrEntity);

    /// <summary>A registry command, prepared for a batch: bound now, its count the driver's.</summary>
    internal PendingWrite PrepareCommand(SqlSource source, object args)
    {
        var command = CreateCommand(source, args);
        return new PendingWrite(command, WriteResult.Count, instance: null, generatesKey: false)
        {
            Name = source.LogName("command"),
            IsCommand = true,
            Complete = result => (int)result!,
            Undo = static () => { },
        };
    }

    /// <summary>
    /// An instance's key when it is set (plan rule 1a): no null part, not a
    /// database-generated key at its default, not an empty client GUID — the
    /// "unset key" of spec/unit-of-work.md "Detach and merge".
    /// </summary>
    internal static EntityKey? SetKeyOf(EntityMap map, object entity)
    {
        if (map.KeyProperties.Count == 0)
        {
            return null;
        }

        var values = map.GetKeyValues(entity);
        if (values.Any(v => v is null)
            || (map.KeyStrategy == KeyStrategy.DatabaseGenerated
                && Convert.ToInt64(values[0], System.Globalization.CultureInfo.InvariantCulture) == 0)
            || (map.KeyStrategy == KeyStrategy.ClientGuid && Equals(values[0], Guid.Empty)))
        {
            return null;
        }

        return new EntityKey(map.EntityType, values);
    }

    /// <summary>A delete by key's identity for rule 1a: its key argument, validated; none when it would not validate (the preparation refuses it).</summary>
    internal static EntityKey? KeyArgumentOf(EntityMap map, object key)
    {
        try
        {
            return new EntityKey(map.EntityType, ValidateKey(map, key));
        }
        catch (SimpleOrmException)
        {
            return null;
        }
    }

    /// <summary>The instances a statement on <paramref name="entity"/> may read the key of (plan rule 1b): its non-null many-to-one targets.</summary>
    internal List<object> ManyToOneTargets(EntityMap map, object entity)
    {
        var targets = new List<object>();
        foreach (var relationship in map.Relationships)
        {
            if (relationship.Kind == RelationshipKind.ManyToOne && NavigationValue(map, relationship, entity) is { } target)
            {
                targets.Add(target);
            }
        }

        return targets;
    }

    /// <summary>
    /// Sends an explicit batch (spec/session.md "The explicit batch"): the queued
    /// operations in order, as messages by the plan, inside the session's
    /// transaction or one of its own; a failure undoes every in-memory effect.
    /// </summary>
    internal async Task<BatchResult> SendAsync(IReadOnlyList<StatementBatch.Queued> queued, CancellationToken ct)
    {
        if (queued.Count == 0)
        {
            return new BatchResult([], 0);   // nothing to send: no statement, no transaction
        }

        var scope = _transaction is null ? await BeginAsync(ct).ConfigureAwait(false) : null;
        var call = new WriteCall(this, ct);
        try
        {
            var writes = new PendingWrite?[queued.Count];
            for (var i = 0; i < queued.Count; i++)
            {
                writes[i] = await call.AddAsync(queued[i].Instance, queued[i].Key, queued[i].Reads, queued[i].Prepare).ConfigureAwait(false);
            }

            await call.FinishAsync().ConfigureAwait(false);
            if (scope is not null)
            {
                await scope.CommitAsync(ct).ConfigureAwait(false);
            }

            return new BatchResult(writes.Select(w => w?.Affected ?? 0).ToArray(), call.Messages);
        }
        catch
        {
            call.Undo();
            throw;
        }
        finally
        {
            call.Dispose();
            if (scope is not null)
            {
                await scope.DisposeAsync().ConfigureAwait(false);   // rolls back unless committed
            }
        }
    }

    /// <summary>
    /// One call's statements — a flush, a batch send — planned into messages and
    /// sent (spec/session.md "Batching"). Under <c>Off</c> every statement is its
    /// own message, prepared and run in turn exactly as the immediate operations.
    /// Under <c>Auto</c> a statement joins the current message unless it depends
    /// on it (the same instance; a key the database generates in it), reads a
    /// result after a registry command, or would overflow it; a message goes out
    /// as one <c>DbBatch</c> where batching is in effect, else statement by
    /// statement reusing one command per text. Every prepared statement stays in
    /// the journal, so a failure anywhere can undo them all.
    /// </summary>
    private sealed class WriteCall(Db db, CancellationToken ct) : IDisposable
    {
        private readonly int _maxStatements = db.Options.Batching == BatchingMode.Auto ? db.Options.MaxStatementsPerMessage : 1;
        private readonly List<PendingWrite> _journal = [];
        private readonly List<PendingWrite> _current = [];
        private readonly Dictionary<string, DbCommand>? _reuse =
            db.Options.Batching == BatchingMode.Auto && !db.BatchingInEffect ? new(StringComparer.Ordinal) : null;
        private int _currentParameters;

        /// <summary>The messages the plan formed so far.</summary>
        public int Messages { get; private set; }

        /// <summary>
        /// Plans one operation. Before it is prepared — its preparation may read what
        /// the current message produces — a full message goes out (rule 3's statement
        /// cap) and so does one it depends on (rule 1); after, rule 2 and the
        /// parameter cap. Under <c>Off</c> the cap is one, so every statement is
        /// prepared and run in turn, exactly as the immediate operations. Returns the
        /// prepared statement, or null when the operation issues none.
        /// </summary>
        public async Task<PendingWrite?> AddAsync(
            object? instance, Func<EntityKey?> key, Func<IReadOnlyCollection<object>> reads, Func<PendingWrite?> prepare)
        {
            if (_current.Count > 0 && (_current.Count >= _maxStatements || DependsOnCurrent(instance, key, reads)))
            {
                await SendCurrentAsync().ConfigureAwait(false);
            }

            var write = prepare();
            if (write is null)
            {
                return null;
            }

            _journal.Add(write);
            if (_current.Count > 0 && (ReadsAfterCommand(write) || OverflowsParameters(write)))
            {
                await SendCurrentAsync().ConfigureAwait(false);
            }

            _current.Add(write);
            _currentParameters += write.Command.Parameters.Count;
            return write;
        }

        public Task FinishAsync() => _current.Count > 0 ? SendCurrentAsync() : Task.CompletedTask;

        /// <summary>Undoes every prepared statement's in-memory effects, last first.</summary>
        public void Undo()
        {
            for (var i = _journal.Count - 1; i >= 0; i--)
            {
                _journal[i].Undo();
            }
        }

        public void Dispose()
        {
            foreach (var write in _journal)
            {
                write.Command.Dispose();
            }
        }

        /// <summary>
        /// Rule 1: the current message holds a statement on the same instance, or on
        /// one of the same type with the same set key — the new statement's key as it
        /// stands before its preparation against the keys the message's statements
        /// were prepared with — or the insert of an instance this one may read the
        /// generated key of.
        /// </summary>
        private bool DependsOnCurrent(object? instance, Func<EntityKey?> key, Func<IReadOnlyCollection<object>> reads)
        {
            if (instance is not null && _current.Any(w => ReferenceEquals(w.Instance, instance)))
            {
                return true;
            }

            if (key() is { } identity
                && _current.Any(w => w.Key is { } other && other.Type == identity.Type
                    && KeyTupleComparer.Instance.Equals(other.Values, identity.Values)))
            {
                return true;
            }

            if (!_current.Any(w => w.GeneratesKey))
            {
                return false;
            }

            var read = reads();
            return read.Count > 0 && _current.Any(w => w.GeneratesKey && read.Any(r => ReferenceEquals(r, w.Instance)));
        }

        /// <summary>Rule 2: a result the session reads must never follow a registry command's own result sets in a message.</summary>
        private bool ReadsAfterCommand(PendingWrite write)
            => write.Result == WriteResult.Scalar && _current.Any(w => w.IsCommand);

        /// <summary>Rule 3, the dialect's parameter cap (the statement cap is checked before preparing).</summary>
        private bool OverflowsParameters(PendingWrite write)
            => db.Options.Dialect.MaxParametersPerMessage is { } cap
                && _currentParameters + write.Command.Parameters.Count > cap;

        private async Task SendCurrentAsync()
        {
            var message = _current.ToArray();
            _current.Clear();
            _currentParameters = 0;
            Messages++;
            db.MessageObserver?.Invoke(message.Length);
#if NET
            if (db.BatchingInEffect && message.Length > 1)
            {
                await SendAsBatchAsync(message).ConfigureAwait(false);
                return;
            }
#endif
            foreach (var write in message)
            {
                write.Affected = write.Complete(await db.ExecuteOneAsync(write, _reuse, ct).ConfigureAwait(false));
            }
        }

#if NET
        /// <summary>
        /// A message as one <c>DbBatch</c>, its results read per statement: the
        /// result sets of the statements that return one, in order, then every
        /// other statement's own count; then each statement judged and applied in
        /// order. A registry command holding several statements goes on its own
        /// (Npgsql refuses several statements in one batch command) — a
        /// realization may take more round trips than the plan, never fewer.
        /// </summary>
        private async Task SendAsBatchAsync(PendingWrite[] message)
        {
            var run = new List<PendingWrite>();
            foreach (var write in message)
            {
                if (write.IsCommand && SqlPlaceholders.HoldsSeveralStatements(write.Command.CommandText))
                {
                    await SendRunAsync(run).ConfigureAwait(false);
                    run.Clear();
                    write.Affected = write.Complete(await db.ExecuteOneAsync(write, reuse: null, ct).ConfigureAwait(false));
                    continue;
                }

                run.Add(write);
            }

            await SendRunAsync(run).ConfigureAwait(false);
        }

        private async Task SendRunAsync(List<PendingWrite> run)
        {
            if (run.Count == 0)
            {
                return;
            }

            if (run.Count == 1)
            {
                run[0].Affected = run[0].Complete(await db.ExecuteOneAsync(run[0], reuse: null, ct).ConfigureAwait(false));
                return;
            }

            using var batch = db._connection.CreateBatch();
            batch.Transaction = db._transaction;
            foreach (var write in run)
            {
                var batchCommand = batch.CreateBatchCommand();
                batchCommand.CommandText = write.Command.CommandText;
                MoveParameters(write.Command, batchCommand.Parameters);
                db.LogStatement(write.Name, batchCommand.CommandText, batchCommand.Parameters);
                batch.BatchCommands.Add(batchCommand);
            }

            var results = new object?[run.Count];
            var reader = await batch.ExecuteReaderAsync(ct).ConfigureAwait(false);
            await using (reader.ConfigureAwait(false))
            {
                var first = true;
                for (var i = 0; i < run.Count; i++)
                {
                    if (run[i].Result != WriteResult.Scalar)
                    {
                        continue;
                    }

                    if (!first && !await reader.NextResultAsync(ct).ConfigureAwait(false))
                    {
                        break;   // a missing result: Complete reports it (never read as zero)
                    }

                    first = false;
                    results[i] = await reader.ReadAsync(ct).ConfigureAwait(false) ? reader.GetValue(0) : null;
                }

                // Step through everything after the last result read: a failure in a
                // statement whose result the session never reads surfaces here, before
                // anything is judged — never discarded with the reader.
                while (await reader.NextResultAsync(ct).ConfigureAwait(false))
                {
                }
            }

            for (var i = 0; i < run.Count; i++)
            {
                if (run[i].Result == WriteResult.Count)
                {
                    results[i] = batch.BatchCommands[i].RecordsAffected;
                }
            }

            for (var i = 0; i < run.Count; i++)
            {
                run[i].Affected = run[i].Complete(results[i]);
            }
        }
#endif
    }
}
