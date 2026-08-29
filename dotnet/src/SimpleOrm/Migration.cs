using System.Text.RegularExpressions;

namespace SimpleOrm;

/// <summary>
/// The recorded unit of schema change (ADR-0013): a root class named
/// <c>V&lt;version&gt;</c> (e.g. <c>V0001</c>) directly under <c>Migrations/</c>,
/// composing per-object migration steps in explicit, reviewable order. One
/// <c>schema_version</c> row is written per (version, object); a version applies
/// atomically. Migrations are code — never external .sql files; apply is always
/// explicit, never at startup. Generation (the diff tool) arrives after milestone 6
/// and authors these same artifacts.
/// </summary>
public abstract class MigrationVersion
{
    private static readonly Regex NamePattern = new(@"^V(\d+)$", RegexOptions.Compiled);

    public virtual long Version
    {
        get
        {
            var match = NamePattern.Match(GetType().Name);
            if (!match.Success)
            {
                throw new SimpleOrmException(
                    "MIG-001", GetType().Name, "root migration class names are V<version>, e.g. V0001");
            }

            return long.Parse(match.Groups[1].Value);
        }
    }

    /// <summary>Applies this version's object steps in order (FK order across tables is the author's/generator's job).</summary>
    public abstract void Compose(VersionBuilder version);
}

/// <summary>Collects a version's object steps in explicit order.</summary>
public sealed class VersionBuilder
{
    internal List<MigrationStep> Steps { get; } = [];

    public VersionBuilder Apply<TStep>()
        where TStep : MigrationStep, new()
        => Apply(new TStep());

    public VersionBuilder Apply(MigrationStep step)
    {
        Steps.Add(step);
        return this;
    }
}

/// <summary>
/// One object's change within a version: a class named
/// <c>V&lt;version&gt;_&lt;Description&gt;</c> in the object's folder
/// (<c>Migrations/Table/User/…</c>). Its version must match the composing root
/// (<c>MIG-003</c>); a step no root composes is an error (<c>MIG-004</c>).
/// </summary>
public abstract class MigrationStep
{
    private static readonly Regex NamePattern = new(@"^V(\d+)_(\w+)$", RegexOptions.Compiled);

    public virtual long Version => Parse().Version;

    public virtual string Description => Parse().Description;

    internal abstract string ObjectName(EntityMapLoader maps);

    internal abstract IReadOnlyList<MigrationStatement> RenderUp(EntityMapLoader maps, IDialect dialect);

    internal abstract IReadOnlyList<MigrationStatement> RenderDown(EntityMapLoader maps, IDialect dialect);

    private (long Version, string Description) Parse()
    {
        var match = NamePattern.Match(GetType().Name);
        if (!match.Success)
        {
            throw new SimpleOrmException(
                "MIG-001", GetType().Name,
                "object migration class names are V<version>_<Description>, e.g. V0002_AddDisplayName");
        }

        return (long.Parse(match.Groups[1].Value), match.Groups[2].Value);
    }
}

/// <summary>A rendered statement with the action it came from, for error reporting.</summary>
internal sealed class MigrationStatement(string sql, string origin)
{
    public string Sql { get; } = sql;

    public string Origin { get; } = origin;
}

/// <summary>A table's change: actions execute rename → add → remove → raw SQL, regardless of call order.</summary>
public abstract class TableMigration<TEntity> : MigrationStep
    where TEntity : class
{
    public abstract void Action(TableActions actions);

    /// <summary>Optional inverse; rendering nothing means not reversible (<c>MIG-020</c> on migrate down).</summary>
    public virtual void Down(TableActions actions)
    {
    }

    internal override string ObjectName(EntityMapLoader maps) => maps.Load<TEntity>().RelationName!;

    internal override IReadOnlyList<MigrationStatement> RenderUp(EntityMapLoader maps, IDialect dialect)
        => Render(maps, dialect, Action);

    internal override IReadOnlyList<MigrationStatement> RenderDown(EntityMapLoader maps, IDialect dialect)
        => Render(maps, dialect, Down);

    private IReadOnlyList<MigrationStatement> Render(
        EntityMapLoader maps, IDialect dialect, Action<TableActions> compose)
    {
        var map = maps.Load<TEntity>();
        if (map.Kind != RelationKind.Table)
        {
            throw new SimpleOrmException(
                "DDL-001", typeof(TEntity).Name, $"is {map.Kind}-backed; TableMigration applies to tables");
        }

        var actions = new TableActions(map, dialect);
        compose(actions);
        return actions.Build();
    }
}

/// <summary>A view's (or materialized view's) change: actions execute in declaration order.</summary>
public abstract class ViewMigration<TEntity> : MigrationStep
    where TEntity : class
{
    public abstract void Action(ViewActions actions);

    public virtual void Down(ViewActions actions)
    {
    }

    internal override string ObjectName(EntityMapLoader maps) => maps.Load<TEntity>().RelationName!;

    internal override IReadOnlyList<MigrationStatement> RenderUp(EntityMapLoader maps, IDialect dialect)
        => Render(maps, dialect, Action);

    internal override IReadOnlyList<MigrationStatement> RenderDown(EntityMapLoader maps, IDialect dialect)
        => Render(maps, dialect, Down);

    private IReadOnlyList<MigrationStatement> Render(
        EntityMapLoader maps, IDialect dialect, Action<ViewActions> compose)
    {
        var map = maps.Load<TEntity>();
        if (map.Kind is not (RelationKind.View or RelationKind.MaterializedView))
        {
            throw new SimpleOrmException(
                "DDL-001", typeof(TEntity).Name, $"is {map.Kind}-backed; ViewMigration applies to views");
        }

        if (map.Kind == RelationKind.MaterializedView && !dialect.SupportsMaterializedViews)
        {
            throw new SimpleOrmException("DDL-002", typeof(TEntity).Name, "the dialect has no materialized views");
        }

        var actions = new ViewActions(map, dialect);
        compose(actions);
        return actions.Build();
    }
}

/// <summary>One action with its optional per-action data hooks (pre runs before it, post after).</summary>
public sealed class MigrationAction
{
    internal MigrationAction(string origin, IReadOnlyList<string> statements)
    {
        Origin = origin;
        Statements = statements;
    }

    internal string Origin { get; }

    internal IReadOnlyList<string> Statements { get; }

    internal List<string> PreSql { get; } = [];

    internal List<string> PostSql { get; } = [];

    /// <summary>Data step executed immediately before this action (e.g. preserve values). Optional; chainable.</summary>
    public MigrationAction Pre(string sql)
    {
        PreSql.Add(sql);
        return this;
    }

    /// <summary>Data step executed immediately after this action (e.g. backfill). Optional; chainable.</summary>
    public MigrationAction Post(string sql)
    {
        PostSql.Add(sql);
        return this;
    }

    internal IEnumerable<MigrationStatement> Render()
    {
        foreach (var sql in PreSql)
        {
            yield return new MigrationStatement(sql, Origin + " pre");
        }

        foreach (var sql in Statements)
        {
            yield return new MigrationStatement(sql, Origin);
        }

        foreach (var sql in PostSql)
        {
            yield return new MigrationStatement(sql, Origin + " post");
        }
    }
}

/// <summary>
/// Table actions. Fixed execution order — renames, then adds, then removes, then raw
/// SQL — with declaration order inside each group; each action carries optional
/// Pre/Post data hooks. Column specs are literal (frozen); metadata-rendered DDL is
/// legal only where the version checksum freezes it (the object's create).
/// </summary>
public sealed class TableActions
{
    private readonly EntityMap _map;
    private readonly IDialect _dialect;
    private readonly List<MigrationAction> _renames = [];
    private readonly List<MigrationAction> _adds = [];
    private readonly List<MigrationAction> _removes = [];
    private readonly List<MigrationAction> _custom = [];

    internal TableActions(EntityMap map, IDialect dialect)
    {
        _map = map;
        _dialect = dialect;
    }

    private string Table => _map.RelationName!;

    /// <summary>The object's initial creation, rendered from metadata (table + declared indexes).</summary>
    public MigrationAction CreateTable()
    {
        var statements = new List<string> { _dialect.CreateTableSql(_map) };
        statements.AddRange(_dialect.CreateIndexSql(_map));
        return Track(_adds, new MigrationAction("create " + Table, statements));
    }

    public MigrationAction DropTable()
        => Track(_removes, new MigrationAction("drop " + Table, ["drop table " + Table]));

    public MigrationAction RenameTable(string fromName)
        => Track(_renames, new MigrationAction(
            $"rename table {fromName}", [$"alter table {fromName} rename to {Table}"]));

    public MigrationAction RenameColumn(string fromName, string toName)
        => Track(_renames, new MigrationAction(
            $"rename {Table}.{fromName}", [$"alter table {Table} rename column {fromName} to {toName}"]));

    /// <summary>Literal column spec; a NOT NULL addition to a populated table needs <paramref name="defaultSql"/>.</summary>
    public MigrationAction AddColumn(string name, string type, bool nullable = true, string? defaultSql = null)
    {
        var sql = $"alter table {Table} add column {name} {type}"
            + (nullable ? string.Empty : " not null")
            + (defaultSql is null ? string.Empty : " default " + defaultSql);
        return Track(_adds, new MigrationAction($"add {Table}.{name}", [sql]));
    }

    public MigrationAction RemoveColumn(string name)
        => Track(_removes, new MigrationAction(
            $"remove {Table}.{name}", [$"alter table {Table} drop column {name}"]));

    public MigrationAction CreateIndexes()
        => Track(_adds, new MigrationAction("indexes " + Table, _dialect.CreateIndexSql(_map)));

    public MigrationAction DropIndex(string name)
        => Track(_removes, new MigrationAction("drop index " + name, ["drop index " + name]));

    /// <summary>Raw SQL escape hatch; runs after the ordered groups.</summary>
    public MigrationAction Sql(string sql)
        => Track(_custom, new MigrationAction("sql " + Table, [sql]));

    internal IReadOnlyList<MigrationStatement> Build()
        => _renames.Concat(_adds).Concat(_removes).Concat(_custom)
            .SelectMany(a => a.Render())
            .ToArray();

    private static MigrationAction Track(List<MigrationAction> group, MigrationAction action)
    {
        group.Add(action);
        return action;
    }
}

/// <summary>View actions; executed in declaration order.</summary>
public sealed class ViewActions
{
    private readonly EntityMap _map;
    private readonly IDialect _dialect;
    private readonly List<MigrationAction> _actions = [];

    internal ViewActions(EntityMap map, IDialect dialect)
    {
        _map = map;
        _dialect = dialect;
    }

    private string View => _map.RelationName!;

    public MigrationAction CreateView()
    {
        var statements = new List<string> { _dialect.CreateViewSql(_map) };
        statements.AddRange(_dialect.CreateIndexSql(_map));
        return Track(new MigrationAction("create " + View, statements));
    }

    public MigrationAction DropView()
        => Track(new MigrationAction("drop " + View, ["drop view " + View]));

    /// <summary>Drops (if present) and re-creates from the current defining SQL.</summary>
    public MigrationAction RecreateView()
    {
        var statements = new List<string> { "drop view if exists " + View, _dialect.CreateViewSql(_map) };
        statements.AddRange(_dialect.CreateIndexSql(_map));
        return Track(new MigrationAction("recreate " + View, statements));
    }

    public MigrationAction Sql(string sql) => Track(new MigrationAction("sql " + View, [sql]));

    internal IReadOnlyList<MigrationStatement> Build()
        => _actions.SelectMany(a => a.Render()).ToArray();

    private MigrationAction Track(MigrationAction action)
    {
        _actions.Add(action);
        return action;
    }
}

/// <summary>
/// A data-driven version — raw SQL steps constructed programmatically (used by the
/// conformance suite; also the shape a future generator can target).
/// </summary>
public sealed class SqlVersion(long version, params SqlVersion.Step[] steps) : MigrationVersion
{
    public override long Version => version;

    public override void Compose(VersionBuilder builder)
    {
        foreach (var step in steps)
        {
            builder.Apply(new RawSqlStep(version, step));
        }
    }

    public sealed class Step(string objectName, string description, IReadOnlyList<string> up, IReadOnlyList<string>? down = null)
    {
        public string ObjectName { get; } = objectName;

        public string Description { get; } = description;

        public IReadOnlyList<string> Up { get; } = up;

        public IReadOnlyList<string> Down { get; } = down ?? [];
    }

    private sealed class RawSqlStep(long version, Step step) : MigrationStep
    {
        public override long Version => version;

        public override string Description => step.Description;

        internal override string ObjectName(EntityMapLoader maps) => step.ObjectName;

        internal override IReadOnlyList<MigrationStatement> RenderUp(EntityMapLoader maps, IDialect dialect)
            => step.Up.Select(s => new MigrationStatement(s, "sql " + step.ObjectName)).ToArray();

        internal override IReadOnlyList<MigrationStatement> RenderDown(EntityMapLoader maps, IDialect dialect)
            => step.Down.Select(s => new MigrationStatement(s, "sql " + step.ObjectName)).ToArray();
    }
}
