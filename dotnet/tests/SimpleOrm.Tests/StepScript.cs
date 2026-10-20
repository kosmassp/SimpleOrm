using System.Collections;
using System.Data.Common;
using System.Reflection;
using System.Text.Json;
using Microsoft.Data.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The step-script engine shared by the tracking-case and flush-case runners
/// (spec/session.md "Conformance: tracking-cases/", spec/unit-of-work.md
/// "Conformance: flush-cases/"): named instances, the step vocabulary, the
/// expectations, and <c>expectError</c> on any step. What the session wrote is
/// observed through the dialect seam by <see cref="RecordingDialect"/>.
/// </summary>
internal sealed class StepScript(Db db, RecordingDialect dialect, string databasePath)
{
    private readonly Dictionary<string, object> _refs = [];
    private DbTransactionScope? _scope;

    public static string ConformanceDirectory()
    {
        var directory = AppContext.BaseDirectory;
        while (!Directory.Exists(Path.Combine(directory, "conformance")))
        {
            directory = Path.GetDirectoryName(directory)
                ?? throw new DirectoryNotFoundException("conformance directory not found above " + AppContext.BaseDirectory);
        }

        return Path.Combine(directory, "conformance");
    }

    /// <summary>Runs every step of a case; each step's recorded statements start empty.</summary>
    public async Task RunAsync(JsonElement root)
    {
        foreach (var step in root.GetProperty("steps").EnumerateArray())
        {
            var expectedError = step.TryGetProperty("expectError", out var code) ? code.GetString() : null;
            string? actualError = null;
            dialect.Statements.Clear();
            try
            {
                await RunStepAsync(step);
            }
            catch (SimpleOrmException exception)
            {
                actualError = exception.Code;
            }
            catch (ConcurrencyException exception)
            {
                actualError = exception.Code;
            }

            Assert.Equal(expectedError, actualError);
        }
    }

    private async Task RunStepAsync(JsonElement step)
    {
        if (step.TryGetProperty("get", out var get))
        {
            var type = ConformanceDatabase.EntityTypes[get.GetProperty("entity").GetString()!];
            object key = get.TryGetProperty("keyOf", out var keyOf)
                ? db.Maps.Load(type).GetKeyValues(_refs[keyOf.GetString()!])[0]!
                : get.GetProperty("key").GetInt64();
            _refs[get.GetProperty("as").GetString()!] = await InvokeWithResultAsync(nameof(Db.GetAsync), type, key);
        }
        else if (step.TryGetProperty("new", out var @new))
        {
            var type = ConformanceDatabase.EntityTypes[@new.GetProperty("entity").GetString()!];
            var entity = Activator.CreateInstance(type)!;
            var map = db.Maps.Load(type);
            if (@new.TryGetProperty("values", out var values))
            {
                foreach (var value in values.EnumerateObject())
                {
                    var property = map.Properties.Single(p => p.PropertyName == value.Name);
                    property.SetValue(entity, ConformanceCrudTests.ConvertTo(value.Value, property.ClrType));
                }
            }

            if (@new.TryGetProperty("navigations", out var navigations))
            {
                foreach (var navigation in navigations.EnumerateObject())
                {
                    SetNavigation(entity, navigation.Name, navigation.Value);
                }
            }

            _refs[@new.GetProperty("as").GetString()!] = entity;
        }
        else if (step.TryGetProperty("set", out var set))
        {
            var entity = Resolve(set.GetProperty("ref").GetString()!);
            var property = db.Maps.Load(entity.GetType()).Properties.Single(p => p.PropertyName == set.GetProperty("property").GetString());
            property.SetValue(entity, ConformanceCrudTests.ConvertTo(set.GetProperty("value"), property.ClrType));
        }
        else if (step.TryGetProperty("insert", out var insert))
        {
            var entity = Resolve(insert.GetProperty("ref").GetString()!);
            await InvokeAsync(nameof(Db.InsertAsync), entity.GetType(), entity);
        }
        else if (step.TryGetProperty("update", out var update))
        {
            var entity = Resolve(update.GetProperty("ref").GetString()!);
            await InvokeAsync(nameof(Db.UpdateAsync), entity.GetType(), entity);
            if (update.TryGetProperty("expectWritten", out var written))
            {
                var expected = written.ValueKind == JsonValueKind.String
                    ? new[] { written.GetString()! }
                    : written.EnumerateArray().Select(w => w.GetString()!).ToArray();
                var updates = dialect.Statements.Where(s => s.Op == "update").ToList();
                Assert.Equal(expected, updates.Count == 0 ? [] : updates[^1].Columns!);
            }
        }
        else if (step.TryGetProperty("updateOnly", out var updateOnly))
        {
            var entity = Resolve(updateOnly.GetProperty("ref").GetString()!);
            var properties = updateOnly.GetProperty("properties").EnumerateArray().Select(p => p.GetString()!).ToList();
            await Unwrap(() => (Task)typeof(Db).GetMethod(nameof(Db.UpdateOnlyAsync))!
                .MakeGenericMethod(entity.GetType())
                .Invoke(db, [entity, properties, CancellationToken.None])!);
        }
        else if (step.TryGetProperty("delete", out var delete))
        {
            if (delete.TryGetProperty("ref", out var byRef))
            {
                var entity = Resolve(byRef.GetString()!);
                await InvokeAsync(nameof(Db.DeleteAsync), entity.GetType(), entity);
            }
            else
            {
                var type = ConformanceDatabase.EntityTypes[delete.GetProperty("entity").GetString()!];
                await InvokeAsync(nameof(Db.DeleteAsync), type, delete.GetProperty("key").GetInt64());
            }
        }
        else if (step.TryGetProperty("refresh", out var refresh))
        {
            var entity = Resolve(refresh.GetProperty("ref").GetString()!);
            await InvokeAsync(nameof(Db.RefreshAsync), entity.GetType(), entity);
        }
        else if (step.TryGetProperty("load", out var load))
        {
            var entity = Resolve(load.GetProperty("ref").GetString()!);
            await Unwrap(() => (Task)typeof(Db).GetMethod(nameof(Db.LoadAsync))!
                .MakeGenericMethod(entity.GetType())
                .Invoke(db, [entity, load.GetProperty("navigation").GetString()!, CancellationToken.None])!);
        }
        else if (step.TryGetProperty("persist", out var persist))
        {
            db.Persist(Resolve(persist.GetProperty("ref").GetString()!));
        }
        else if (step.TryGetProperty("remove", out var remove))
        {
            db.Remove(Resolve(remove.GetProperty("ref").GetString()!));
        }
        else if (step.TryGetProperty("detach", out var detach))
        {
            db.Detach(Resolve(detach.GetProperty("ref").GetString()!));
        }
        else if (step.TryGetProperty("merge", out var merge))
        {
            var detached = Resolve(merge.GetProperty("ref").GetString()!);
            _refs[merge.GetProperty("as").GetString()!] = await InvokeWithResultAsync(nameof(Db.MergeAsync), detached.GetType(), detached);
        }
        else if (step.TryGetProperty("flush", out var flush))
        {
            var summary = await db.FlushAsync(CancellationToken.None);
            if (flush.TryGetProperty("expectStatements", out var statements))
            {
                var expected = statements.EnumerateArray().Select(s => new Statement(
                    s.GetProperty("op").GetString()!,
                    s.GetProperty("entity").GetString()!,
                    s.TryGetProperty("columns", out var columns)
                        ? columns.ValueKind == JsonValueKind.String ? [columns.GetString()!] : columns.EnumerateArray().Select(c => c.GetString()!).ToArray()
                        : null)).ToList();
                Assert.Equal(expected.Select(Describe), dialect.Statements.Select(Describe));
            }

            if (flush.TryGetProperty("expectSummary", out var expectedSummary))
            {
                Assert.Equal(
                    new FlushSummary(
                        expectedSummary.GetProperty("inserted").GetInt32(),
                        expectedSummary.GetProperty("updated").GetInt32(),
                        expectedSummary.GetProperty("deleted").GetInt32()),
                    summary);
            }
        }
        else if (step.TryGetProperty("begin", out _))
        {
            _scope = await db.BeginAsync(CancellationToken.None);
        }
        else if (step.TryGetProperty("commit", out _))
        {
            await _scope!.CommitAsync(CancellationToken.None);
            _scope = null;
        }
        else if (step.TryGetProperty("rollback", out _))
        {
            await _scope!.RollbackAsync(CancellationToken.None);
            _scope = null;
        }
        else if (step.TryGetProperty("expect", out var expect))
        {
            Expect(expect);
        }
        else
        {
            throw new InvalidOperationException("unknown step: " + step.GetRawText());
        }
    }

    private void Expect(JsonElement expect)
    {
        if (expect.TryGetProperty("same", out var same))
        {
            var pair = same.EnumerateArray().Select(r => Resolve(r.GetString()!)).ToArray();
            Assert.Same(pair[0], pair[1]);
        }
        else if (expect.TryGetProperty("notSame", out var notSame))
        {
            var pair = notSame.EnumerateArray().Select(r => Resolve(r.GetString()!)).ToArray();
            Assert.NotSame(pair[0], pair[1]);
        }
        else if (expect.TryGetProperty("dirty", out var dirty))
        {
            var entity = Resolve(dirty.GetProperty("ref").GetString()!);
            var changes = (IReadOnlyList<string>)Unwrap(() => typeof(Db).GetMethod(nameof(Db.ChangesOf))!
                .MakeGenericMethod(entity.GetType())
                .Invoke(db, [entity])!);
            Assert.Equal(dirty.GetProperty("properties").EnumerateArray().Select(p => p.GetString()!).ToArray(), changes);
        }
        else if (expect.TryGetProperty("value", out var value))
        {
            var entity = Resolve(value.GetProperty("ref").GetString()!);
            var map = db.Maps.Load(entity.GetType());
            var name = value.GetProperty("property").GetString()!;
            var property = map.Properties.SingleOrDefault(p => p.PropertyName == name);
            var expected = value.GetProperty("equals");
            // An owned navigation named whole: null, or {} for "an owned object exists" (spec/session.md "The step scripts").
            var actual = property is not null
                ? ConformanceCrudTests.Encode(property.GetValue(entity))
                : map.Properties.Select(p => p.Owner).OfType<OwnedMap>().First(o => o.PropertyName == name).Property.GetValue(entity) is null ? null : "{}";
            Assert.Equal(
                expected.ValueKind switch
                {
                    JsonValueKind.Number => expected.GetRawText(),
                    JsonValueKind.True => "true",
                    JsonValueKind.False => "false",
                    JsonValueKind.Null => null,
                    JsonValueKind.Object => "{}",
                    _ => expected.GetString(),
                },
                actual);
        }
        else if (expect.TryGetProperty("sameValue", out var sameValue))
        {
            var pair = sameValue.EnumerateArray().Select(r => ConformanceCrudTests.Encode(Resolve(r.GetString()!))).ToArray();
            Assert.Equal(pair[0], pair[1]);
        }
        else if (expect.TryGetProperty("tracked", out var tracked))
        {
            var entity = Resolve(tracked.GetProperty("ref").GetString()!);
            Assert.Equal(tracked.GetProperty("is").GetBoolean(), db.IsTracked(entity));
        }
        else if (expect.TryGetProperty("pending", out var pending))
        {
            Assert.Equal(pending.GetBoolean(), db.HasPendingWork);
        }
        else if (expect.TryGetProperty("count", out var count))
        {
            var map = db.Maps.Load(ConformanceDatabase.EntityTypes[count.GetProperty("entity").GetString()!]);
            using var connection = new SqliteConnection($"Data Source={databasePath}");
            connection.Open();
            using var command = connection.CreateCommand();
            command.CommandText = "select count(*) from " + map.RelationName;   // metadata, not user data; a test-side count
            Assert.Equal(count.GetProperty("equals").GetInt64(), (long)command.ExecuteScalar()!);
        }
        else
        {
            throw new InvalidOperationException("unknown expectation: " + expect.GetRawText());
        }
    }

    /// <summary>A navigation of a fresh instance: one ref, or a list of refs for a collection (typed after the property's element type).</summary>
    private void SetNavigation(object entity, string name, JsonElement value)
    {
        var property = entity.GetType().GetProperty(name)
            ?? throw new InvalidOperationException($"{entity.GetType().Name} has no navigation {name}");
        if (value.ValueKind == JsonValueKind.String)
        {
            property.SetValue(entity, _refs[value.GetString()!]);
            return;
        }

        var elementType = property.PropertyType.GetGenericArguments()[0];
        var list = (IList)Activator.CreateInstance(typeof(List<>).MakeGenericType(elementType))!;
        foreach (var item in value.EnumerateArray())
        {
            list.Add(_refs[item.GetString()!]);
        }

        property.SetValue(entity, list);
    }

    /// <summary>A ref, optionally hopping one member (<c>t.User</c>, <c>t.UserId</c>) or indexing a loaded collection (<c>a.Transactions[0]</c>).</summary>
    private object Resolve(string path)
    {
        var dot = path.IndexOf('.');
        if (dot < 0)
        {
            return _refs[path];
        }

        var owner = _refs[path[..dot]];
        var member = path[(dot + 1)..];
        var bracket = member.IndexOf('[');
        var index = bracket < 0 ? -1 : int.Parse(member[(bracket + 1)..^1]);
        var name = bracket < 0 ? member : member[..bracket];
        var value = owner.GetType().GetProperty(name)!.GetValue(owner)
            ?? throw new InvalidOperationException($"{path} is null");
        return index < 0 ? value : ((IList)value)[index]!;
    }

    private static string Describe(Statement statement)
        => statement.Columns is null
            ? $"{statement.Op} {statement.Entity}"
            : $"{statement.Op} {statement.Entity} [{string.Join(", ", statement.Columns)}]";

    private async Task InvokeAsync(string method, Type entityType, object argument)
        => await Unwrap(() => (Task)typeof(Db).GetMethods()
            .Single(m => m.Name == method && m.GetGenericArguments().Length == 1 && m.GetParameters().Length == 2)
            .MakeGenericMethod(entityType)
            .Invoke(db, [argument, CancellationToken.None])!);

    private async Task<object> InvokeWithResultAsync(string method, Type entityType, object argument)
    {
        var task = (Task)Unwrap(() => typeof(Db).GetMethods()
            .Single(m => m.Name == method && m.GetGenericArguments().Length == 1 && m.GetParameters().Length == 2)
            .MakeGenericMethod(entityType)
            .Invoke(db, [argument, CancellationToken.None])!);
        await Unwrap(() => task);
        return task.GetType().GetProperty("Result")!.GetValue(task)!;
    }

    private static async Task Unwrap(Func<Task> call)
    {
        try
        {
            await call();
        }
        catch (TargetInvocationException exception) when (exception.InnerException is not null)
        {
            throw exception.InnerException;
        }
    }

    private static object Unwrap(Func<object> call)
    {
        try
        {
            return call();
        }
        catch (TargetInvocationException exception) when (exception.InnerException is not null)
        {
            throw exception.InnerException;
        }
    }
}

/// <summary>One statement the session asked the dialect to render: the op, the entity, and for updates the property list (or <c>all</c>).</summary>
internal sealed record Statement(string Op, string Entity, string[]? Columns);

/// <summary>Records every generated statement the dialect is asked to render — the seam where every implementation renders SQL.</summary>
internal sealed class RecordingDialect(IDialect inner) : IDialect
{
    public List<Statement> Statements { get; } = [];

    public string InsertSql(EntityMap map)
    {
        Statements.Add(new Statement("insert", map.EntityType.Name, null));
        return inner.InsertSql(map);
    }

    public string UpdateSql(EntityMap map)
    {
        Statements.Add(new Statement("update", map.EntityType.Name, ["all"]));
        return inner.UpdateSql(map);
    }

    public string UpdateOnlySql(EntityMap map, IReadOnlyList<PropertyMap> properties)
    {
        Statements.Add(new Statement("update", map.EntityType.Name, properties.Select(p => p.PropertyName).ToArray()));
        return inner.UpdateOnlySql(map, properties);
    }

    public string DeleteSql(EntityMap map, bool checkVersion)
    {
        Statements.Add(new Statement("delete", map.EntityType.Name, null));
        return inner.DeleteSql(map, checkVersion);
    }

    public DbConnection CreateConnection(string connectionString) => inner.CreateConnection(connectionString);
    public string QuoteIdentifier(string identifier) => inner.QuoteIdentifier(identifier);
    public string CreateTableSql(EntityMap map) => inner.CreateTableSql(map);
    public IReadOnlyList<string> CreateIndexSql(EntityMap map) => inner.CreateIndexSql(map);
    public string CreateViewSql(EntityMap map) => inner.CreateViewSql(map);
    public string LimitOffsetClause(string? limitParameter, string? offsetParameter) => inner.LimitOffsetClause(limitParameter, offsetParameter);
    public bool PagingRequiresOrderBy => inner.PagingRequiresOrderBy;
    public bool SupportsRowValueIn => inner.SupportsRowValueIn;

    public bool ReportsAffectedRowsBySelect => inner.ReportsAffectedRowsBySelect;
    public string SelectSql(SelectAst select, BindCriteriaParameter bindParameter) => inner.SelectSql(select, bindParameter);
    public bool SupportsArrayParameters => inner.SupportsArrayParameters;
    public bool BindsTemporalsNatively => inner.BindsTemporalsNatively;
    public string ColumnsInfoSql => inner.ColumnsInfoSql;
    public bool IsDeclaredTypeCompatible(string declaredType, Type clrType, bool enumAsInt) => inner.IsDeclaredTypeCompatible(declaredType, clrType, enumAsInt);
    public string ViewDefinitionSql => inner.ViewDefinitionSql;
    public string IndexesInfoSql => inner.IndexesInfoSql;
    public string StorageType(PropertyMap property) => inner.StorageType(property);
    public bool SupportsMaterializedViews => inner.SupportsMaterializedViews;
    public bool SupportsProcedures => inner.SupportsProcedures;
    public bool SupportsTransactionalDdl => inner.SupportsTransactionalDdl;
    public DbTransaction BeginMigrationRunLock(DbConnection connection) => inner.BeginMigrationRunLock(connection);
    public string VersionTableSql => inner.VersionTableSql;
    public string RenameTableSql(string fromName, string toName) => inner.RenameTableSql(fromName, toName);
    public string RenameColumnSql(string table, string fromName, string toName) => inner.RenameColumnSql(table, fromName, toName);
    public string AddColumnSql(string table, string column, string storageType, bool nullable, string? defaultSql) => inner.AddColumnSql(table, column, storageType, nullable, defaultSql);
    public string DropColumnSql(string table, string column) => inner.DropColumnSql(table, column);
    public string DropTableSql(string table) => inner.DropTableSql(table);
    public string DropIndexSql(string table, string index) => inner.DropIndexSql(table, index);
}
