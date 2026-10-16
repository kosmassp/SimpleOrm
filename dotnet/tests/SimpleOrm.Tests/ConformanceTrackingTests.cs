using System.Data.Common;
using System.Reflection;
using System.Text.Json;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The tracking-case runner (§9, Level 3 M1 — ADR-0034 add.1): each
/// <c>conformance/tracking-cases/*.json</c> opens the seeded fixture database in
/// the declared tracking mode and replays a step script against named instances
/// (spec/session.md "Tracking"). What an update wrote is observed through the
/// dialect seam — the only place SQL is rendered in every implementation — by a
/// recording decorator around <see cref="SqliteDialect"/>.
/// </summary>
public sealed class ConformanceTrackingTests
{
    public static TheoryData<string> CaseFiles
    {
        get
        {
            var data = new TheoryData<string>();
            foreach (var file in Directory.GetFiles(Path.Combine(ConformanceDirectory(), "tracking-cases"), "*.json"))
            {
                data.Add(Path.GetFileName(file));
            }

            return data;
        }
    }

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public async Task Case_behaves_as_specified(string fileName)
    {
        using var document = JsonDocument.Parse(
            File.ReadAllText(Path.Combine(ConformanceDirectory(), "tracking-cases", fileName)));
        var root = document.RootElement;
        var path = Path.Combine(Path.GetTempPath(), $"simpleorm_tracking_{Guid.NewGuid():N}.db");
        try
        {
            await ConformanceDatabase.BuildAsync(path, ConformanceDirectory());
            var dialect = new RecordingDialect(new SqliteDialect());
            var mode = root.GetProperty("tracking").GetString() == "tracked" ? TrackingMode.Tracked : TrackingMode.Untracked;
            await using var db = await Db.OpenAsync(
                $"Data Source={path}", new DbOptions { Dialect = dialect, Tracking = mode }, CancellationToken.None);

            var refs = new Dictionary<string, object>();
            foreach (var step in root.GetProperty("steps").EnumerateArray())
            {
                var expectedError = step.TryGetProperty("expectError", out var code) ? code.GetString() : null;
                string? actualError = null;
                dialect.Written.Clear();
                try
                {
                    await RunStepAsync(db, refs, step);
                }
                catch (SimpleOrmException exception)
                {
                    actualError = exception.Code;
                }

                Assert.Equal(expectedError, actualError);
                if (actualError is null && step.TryGetProperty("update", out var update)
                    && update.TryGetProperty("expectWritten", out var written))
                {
                    var expected = written.ValueKind == JsonValueKind.String
                        ? new[] { written.GetString()! }
                        : written.EnumerateArray().Select(w => w.GetString()!).ToArray();
                    Assert.Equal(expected, dialect.Written.Count == 0 ? [] : dialect.Written[^1]);
                }
            }
        }
        finally
        {
            SqliteConnection.ClearAllPools();
            File.Delete(path);
        }
    }

    private static async Task RunStepAsync(Db db, Dictionary<string, object> refs, JsonElement step)
    {
        if (step.TryGetProperty("get", out var get))
        {
            var type = ConformanceDatabase.EntityTypes[get.GetProperty("entity").GetString()!];
            object key = get.TryGetProperty("keyOf", out var keyOf)
                ? db.Maps.Load(type).GetKeyValues(refs[keyOf.GetString()!])[0]!
                : get.GetProperty("key").GetInt64();
            refs[get.GetProperty("as").GetString()!] = await InvokeWithResultAsync(db, nameof(Db.GetAsync), type, key);
        }
        else if (step.TryGetProperty("new", out var @new))
        {
            var type = ConformanceDatabase.EntityTypes[@new.GetProperty("entity").GetString()!];
            var entity = Activator.CreateInstance(type)!;
            var map = db.Maps.Load(type);
            foreach (var value in @new.GetProperty("values").EnumerateObject())
            {
                var property = map.Properties.Single(p => p.PropertyName == value.Name);
                property.SetValue(entity, ConformanceCrudTests.ConvertTo(value.Value, property.ClrType));
            }

            refs[@new.GetProperty("as").GetString()!] = entity;
        }
        else if (step.TryGetProperty("set", out var set))
        {
            var entity = Resolve(refs, set.GetProperty("ref").GetString()!);
            var property = db.Maps.Load(entity.GetType()).Properties.Single(p => p.PropertyName == set.GetProperty("property").GetString());
            property.SetValue(entity, ConformanceCrudTests.ConvertTo(set.GetProperty("value"), property.ClrType));
        }
        else if (step.TryGetProperty("insert", out var insert))
        {
            var entity = Resolve(refs, insert.GetProperty("ref").GetString()!);
            await InvokeAsync(db, nameof(Db.InsertAsync), entity.GetType(), entity);
        }
        else if (step.TryGetProperty("update", out var update))
        {
            var entity = Resolve(refs, update.GetProperty("ref").GetString()!);
            await InvokeAsync(db, nameof(Db.UpdateAsync), entity.GetType(), entity);
        }
        else if (step.TryGetProperty("updateOnly", out var updateOnly))
        {
            var entity = Resolve(refs, updateOnly.GetProperty("ref").GetString()!);
            var properties = updateOnly.GetProperty("properties").EnumerateArray().Select(p => p.GetString()!).ToList();
            await Unwrap(() => (Task)typeof(Db).GetMethod(nameof(Db.UpdateOnlyAsync))!
                .MakeGenericMethod(entity.GetType())
                .Invoke(db, [entity, properties, CancellationToken.None])!);
        }
        else if (step.TryGetProperty("delete", out var delete))
        {
            if (delete.TryGetProperty("ref", out var byRef))
            {
                var entity = Resolve(refs, byRef.GetString()!);
                await InvokeAsync(db, nameof(Db.DeleteAsync), entity.GetType(), entity);
            }
            else
            {
                var type = ConformanceDatabase.EntityTypes[delete.GetProperty("entity").GetString()!];
                await InvokeAsync(db, nameof(Db.DeleteAsync), type, delete.GetProperty("key").GetInt64());
            }
        }
        else if (step.TryGetProperty("refresh", out var refresh))
        {
            var entity = Resolve(refs, refresh.GetProperty("ref").GetString()!);
            await InvokeAsync(db, nameof(Db.RefreshAsync), entity.GetType(), entity);
        }
        else if (step.TryGetProperty("load", out var load))
        {
            var entity = Resolve(refs, load.GetProperty("ref").GetString()!);
            await Unwrap(() => (Task)typeof(Db).GetMethod(nameof(Db.LoadAsync))!
                .MakeGenericMethod(entity.GetType())
                .Invoke(db, [entity, load.GetProperty("navigation").GetString()!, CancellationToken.None])!);
        }
        else if (step.TryGetProperty("expect", out var expect))
        {
            Expect(db, refs, expect);
        }
        else
        {
            throw new InvalidOperationException("unknown step: " + step.GetRawText());
        }
    }

    private static void Expect(Db db, Dictionary<string, object> refs, JsonElement expect)
    {
        if (expect.TryGetProperty("same", out var same))
        {
            var pair = same.EnumerateArray().Select(r => Resolve(refs, r.GetString()!)).ToArray();
            Assert.Same(pair[0], pair[1]);
        }
        else if (expect.TryGetProperty("notSame", out var notSame))
        {
            var pair = notSame.EnumerateArray().Select(r => Resolve(refs, r.GetString()!)).ToArray();
            Assert.NotSame(pair[0], pair[1]);
        }
        else if (expect.TryGetProperty("dirty", out var dirty))
        {
            var entity = Resolve(refs, dirty.GetProperty("ref").GetString()!);
            var changes = (IReadOnlyList<string>)Unwrap(() => typeof(Db).GetMethod(nameof(Db.ChangesOf))!
                .MakeGenericMethod(entity.GetType())
                .Invoke(db, [entity])!);
            Assert.Equal(dirty.GetProperty("properties").EnumerateArray().Select(p => p.GetString()!).ToArray(), changes);
        }
        else if (expect.TryGetProperty("value", out var value))
        {
            var entity = Resolve(refs, value.GetProperty("ref").GetString()!);
            var property = db.Maps.Load(entity.GetType()).Properties.Single(p => p.PropertyName == value.GetProperty("property").GetString());
            var expected = value.GetProperty("equals");
            Assert.Equal(
                expected.ValueKind == JsonValueKind.Number ? expected.GetRawText() : expected.GetString(),
                ConformanceCrudTests.Encode(property.GetValue(entity)));
        }
        else if (expect.TryGetProperty("tracked", out var tracked))
        {
            var entity = Resolve(refs, tracked.GetProperty("ref").GetString()!);
            Assert.Equal(tracked.GetProperty("is").GetBoolean(), db.IsTracked(entity));
        }
        else
        {
            throw new InvalidOperationException("unknown expectation: " + expect.GetRawText());
        }
    }

    /// <summary>A ref, optionally hopping one navigation (<c>t.User</c>) or indexing a loaded collection (<c>a.Transactions[0]</c>).</summary>
    private static object Resolve(Dictionary<string, object> refs, string path)
    {
        var dot = path.IndexOf('.');
        if (dot < 0)
        {
            return refs[path];
        }

        var owner = refs[path[..dot]];
        var member = path[(dot + 1)..];
        var bracket = member.IndexOf('[');
        var index = bracket < 0 ? -1 : int.Parse(member[(bracket + 1)..^1]);
        var name = bracket < 0 ? member : member[..bracket];
        var value = owner.GetType().GetProperty(name)!.GetValue(owner)
            ?? throw new InvalidOperationException($"{path} is null");
        return index < 0 ? value : ((System.Collections.IList)value)[index]!;
    }

    private static async Task InvokeAsync(Db db, string method, Type entityType, object argument)
        => await Unwrap(() => (Task)typeof(Db).GetMethods()
            .Single(m => m.Name == method && m.GetGenericArguments().Length == 1 && m.GetParameters().Length == 2)
            .MakeGenericMethod(entityType)
            .Invoke(db, [argument, CancellationToken.None])!);

    private static async Task<object> InvokeWithResultAsync(Db db, string method, Type entityType, object argument)
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

    private static string ConformanceDirectory()
    {
        var directory = AppContext.BaseDirectory;
        while (!Directory.Exists(Path.Combine(directory, "conformance")))
        {
            directory = Path.GetDirectoryName(directory)
                ?? throw new DirectoryNotFoundException("conformance directory not found above " + AppContext.BaseDirectory);
        }

        return Path.Combine(directory, "conformance");
    }

    /// <summary>Records what each generated update was asked to write: the property list of a column-list update, or <c>all</c> for a full row.</summary>
    private sealed class RecordingDialect(IDialect inner) : IDialect
    {
        public List<string[]> Written { get; } = [];

        public string UpdateSql(EntityMap map)
        {
            Written.Add(["all"]);
            return inner.UpdateSql(map);
        }

        public string UpdateOnlySql(EntityMap map, IReadOnlyList<PropertyMap> properties)
        {
            Written.Add(properties.Select(p => p.PropertyName).ToArray());
            return inner.UpdateOnlySql(map, properties);
        }

        public DbConnection CreateConnection(string connectionString) => inner.CreateConnection(connectionString);
        public string QuoteIdentifier(string identifier) => inner.QuoteIdentifier(identifier);
        public string CreateTableSql(EntityMap map) => inner.CreateTableSql(map);
        public IReadOnlyList<string> CreateIndexSql(EntityMap map) => inner.CreateIndexSql(map);
        public string CreateViewSql(EntityMap map) => inner.CreateViewSql(map);
        public string LimitOffsetClause(string? limitParameter, string? offsetParameter) => inner.LimitOffsetClause(limitParameter, offsetParameter);
        public bool PagingRequiresOrderBy => inner.PagingRequiresOrderBy;
        public bool SupportsRowValueIn => inner.SupportsRowValueIn;
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
        public string InsertSql(EntityMap map) => inner.InsertSql(map);
        public string DeleteSql(EntityMap map, bool checkVersion) => inner.DeleteSql(map, checkVersion);
    }
}
