namespace SimpleOrm;

/// <summary>
/// An explicit batch (Level 4, ADR-0044, spec/session.md "The explicit batch"):
/// a queue of writes and registry commands bound to one session, sent by one
/// visible call. Queuing does no I/O, binds no value, and checks nothing but the
/// batch's state; <see cref="SendAsync"/> runs the operations in queue order,
/// each exactly as its immediate call would at that point of the sequence, as
/// messages by the batching plan — one statement per message under
/// <see cref="BatchingMode.Off"/> — inside the session's transaction or one of
/// its own. Keys never flow between queued operations (the immediate calls never
/// flow them; the unit of work does). A batch sent, or disposed, is spent:
/// queuing on it or sending it again is <c>TX-003</c>; disposed unsent, it is
/// dropped silently. Reads are not batched.
/// </summary>
public sealed class StatementBatch : IAsyncDisposable
{
    private readonly Db _db;
    private readonly List<Queued> _queued = [];
    private bool _spent;

    internal StatementBatch(Db db) => _db = db;

    /// <summary>The operations queued so far.</summary>
    public int Count => _queued.Count;

    /// <summary>Queues the generated insert (<see cref="Db.InsertAsync{TEntity}"/>).</summary>
    public void Insert<TEntity>(TEntity entity)
        where TEntity : class
    {
        Require(entity);
        Enqueue(entity, entity, () => _db.Maps.Load<TEntity>(), map => _db.PrepareInsertFor(map, entity), map => Db.SetKeyOf(map, entity));
    }

    /// <summary>Queues the full-row update (<see cref="Db.UpdateAsync{TEntity}"/>; a clean tracked instance issues nothing).</summary>
    public void Update<TEntity>(TEntity entity)
        where TEntity : class
    {
        Require(entity);
        Enqueue(entity, entity, () => _db.Maps.Load<TEntity>(), _ => _db.PrepareUpdateFor(entity), map => Db.SetKeyOf(map, entity));
    }

    /// <summary>Queues the update by column list (<see cref="Db.UpdateOnlyAsync{TEntity}"/>).</summary>
    public void UpdateOnly<TEntity>(TEntity entity, IReadOnlyList<string> properties)
        where TEntity : class
    {
        Require(entity);
        Enqueue(entity, entity, () => _db.Maps.Load<TEntity>(), _ => _db.PrepareUpdateOnlyFor(entity, properties), map => Db.SetKeyOf(map, entity));
    }

    /// <summary>Queues the delete (<see cref="Db.DeleteAsync{TEntity}"/>): by key, or the entity's version-checked form.</summary>
    public void Delete<TEntity>(object keyOrEntity)
        where TEntity : class
    {
        Require(keyOrEntity);
        var entity = keyOrEntity as TEntity;
        Enqueue(
            entity, entity, () => _db.Maps.Load<TEntity>(), _ => _db.PrepareDeleteFor<TEntity>(keyOrEntity),
            map => entity is not null ? Db.SetKeyOf(map, entity) : Db.KeyArgumentOf(map, keyOrEntity));
    }

    /// <summary>Queues a registry command (<see cref="Db.ExecuteAsync{TArgs}"/>); its count is the driver's.</summary>
    public void Execute<TArgs>(Command<TArgs> command, TArgs args)
    {
        Require(command);
        RequireOpen();
        _queued.Add(new Queued(null, static () => null, static () => [], () => _db.PrepareCommand(command.Source, args!)));
    }

    /// <summary>
    /// Sends the queued operations — the visible round trip(s). Returns one count
    /// per operation in queue order (an insert 1, an update or delete its judged
    /// count, a command the driver's count, an operation that issued no statement
    /// 0) and the number of messages the plan formed. The batch is spent whether
    /// the send succeeds or fails.
    /// </summary>
    public async Task<BatchResult> SendAsync(CancellationToken ct)
    {
        RequireOpen();
        _spent = true;
        return await _db.SendAsync(_queued, ct).ConfigureAwait(false);
    }

    /// <summary>Spends the batch; anything still queued is dropped — nothing is ever sent implicitly.</summary>
    public ValueTask DisposeAsync()
    {
        _spent = true;
        _queued.Clear();
        return default;
    }

    private void Enqueue(
        object? instance, object? navigationsOf, Func<EntityMap> map, Func<EntityMap, PendingWrite?> prepare, Func<EntityMap, EntityKey?> key)
    {
        RequireOpen();
        _queued.Add(new Queued(
            instance,
            () => key(map()),
            () => navigationsOf is null ? [] : _db.ManyToOneTargets(map(), navigationsOf),
            () => prepare(map())));
    }

    private void RequireOpen()
    {
        if (_spent)
        {
            throw new SimpleOrmException(
                "TX-003", "batch", "the batch was already sent or disposed; open a new one with Db.Batch()");
        }
    }

    private static void Require(object? argument)
    {
        if (argument is null)
        {
            throw new ArgumentNullException(nameof(argument));
        }
    }

    /// <summary>One queued operation: the instance it writes and its set key (plan rule 1a), the instances whose keys it may read (rule 1b), and its preparation.</summary>
    internal sealed record Queued(
        object? Instance, Func<EntityKey?> Key, Func<IReadOnlyCollection<object>> Reads, Func<PendingWrite?> Prepare);
}

/// <summary>
/// What one <see cref="StatementBatch.SendAsync"/> did: <see cref="Affected"/>,
/// one count per queued operation in queue order, and <see cref="Messages"/>, the
/// messages the batching plan formed (one per statement under
/// <see cref="BatchingMode.Off"/>; where <see cref="Db.BatchingInEffect"/> is
/// false their statements ran one by one).
/// </summary>
public sealed record BatchResult(IReadOnlyList<int> Affected, int Messages);
