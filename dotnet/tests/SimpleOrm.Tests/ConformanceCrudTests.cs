using System.Globalization;
using System.Reflection;
using System.Text.Json;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The crud-case runner (§9, milestone 7): generated insert/get/update/delete plus
/// optimistic concurrency, replayed as data against a fresh migrated database.
/// Snapshots ("as"/"from") capture entity instances so stale-version conflicts are
/// expressible; "$last" is the most recent insert's key.
/// </summary>
public sealed class ConformanceCrudTests
{
    private static readonly Dictionary<string, Type> EntityTypes = new()
    {
        ["User"] = typeof(User),
        ["Transaction"] = typeof(Transaction),
    };

    public static TheoryData<string> CaseFiles
    {
        get
        {
            var data = new TheoryData<string>();
            foreach (var file in Directory.GetFiles(Path.Combine(ConformanceDirectory(), "crud-cases"), "*.json"))
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
            File.ReadAllText(Path.Combine(ConformanceDirectory(), "crud-cases", fileName)));
        var path = Path.Combine(Path.GetTempPath(), $"simpleorm_crud_{Guid.NewGuid():N}.db");
        try
        {
            await using var db = await Db.OpenAsync(
                $"Data Source={path}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);
            await new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations")
                .MigrateAsync(CancellationToken.None);

            object? lastKey = null;
            var snapshots = new Dictionary<string, object>();

            foreach (var step in document.RootElement.GetProperty("steps").EnumerateArray())
            {
                var expectedError = step.TryGetProperty("expect", out var expect)
                    && expect.TryGetProperty("error", out var code) ? code.GetString() : null;
                string? actualError = null;
                object? result = null;

                try
                {
                    switch (step.GetProperty("op").GetString())
                    {
                        case "insert":
                        {
                            var (type, entity) = NewEntity(db, step);
                            await InvokeAsync(db, nameof(Db.InsertAsync), type, entity);
                            lastKey = db.Maps.Load(type).GetKeyValues(entity)[0];
                            break;
                        }

                        case "get":
                        {
                            var type = EntityTypes[step.GetProperty("entity").GetString()!];
                            result = await InvokeWithResultAsync(db, nameof(Db.GetAsync), type, KeyOf(step, lastKey));
                            if (step.TryGetProperty("as", out var name))
                            {
                                snapshots[name.GetString()!] = result!;
                            }

                            break;
                        }

                        case "update":
                        {
                            var entity = snapshots[step.GetProperty("from").GetString()!];
                            ApplyValues(db, entity, step.GetProperty("values"));
                            await InvokeAsync(db, nameof(Db.UpdateAsync), entity.GetType(), entity);
                            break;
                        }

                        case "delete":
                        {
                            var (type, target) = step.TryGetProperty("from", out var from)
                                ? (snapshots[from.GetString()!].GetType(), snapshots[from.GetString()!])
                                : (EntityTypes[step.GetProperty("entity").GetString()!], KeyOf(step, lastKey));
                            await InvokeAsync(db, nameof(Db.DeleteAsync), type, target);
                            break;
                        }

                        default:
                            throw new InvalidOperationException("unknown op");
                    }
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
                if (result is not null && expectedError is null
                    && step.TryGetProperty("expect", out var expectation)
                    && expectation.TryGetProperty("values", out var expectedValues))
                {
                    AssertValues(db, result, expectedValues);
                }
            }
        }
        finally
        {
            SqliteConnection.ClearAllPools();
            File.Delete(path);
        }
    }

    private static object KeyOf(JsonElement step, object? lastKey)
    {
        var key = step.GetProperty("key");
        return key.ValueKind == JsonValueKind.String && key.GetString() == "$last"
            ? lastKey ?? throw new InvalidOperationException("no prior insert for $last")
            : key.GetInt64();
    }

    private static (Type Type, object Entity) NewEntity(Db db, JsonElement step)
    {
        var type = EntityTypes[step.GetProperty("entity").GetString()!];
        var entity = Activator.CreateInstance(type)!;
        ApplyValues(db, entity, step.GetProperty("values"));
        return (type, entity);
    }

    private static void ApplyValues(Db db, object entity, JsonElement values)
    {
        var map = db.Maps.Load(entity.GetType());
        foreach (var value in values.EnumerateObject())
        {
            var property = map.Properties.Single(p => p.ColumnName == value.Name);
            property.Property.SetValue(entity, ConvertTo(value.Value, property.ClrType));
        }
    }

    private static void AssertValues(Db db, object entity, JsonElement expected)
    {
        var map = db.Maps.Load(entity.GetType());
        foreach (var value in expected.EnumerateObject())
        {
            var property = map.Properties.Single(p => p.ColumnName == value.Name);
            var actual = Encode(property.Property.GetValue(entity));
            var expectedText = value.Value.ValueKind switch
            {
                JsonValueKind.Null => null,
                JsonValueKind.Number => value.Value.GetRawText(),
                _ => value.Value.GetString(),
            };
            Assert.Equal(expectedText, actual);
        }
    }

    /// <summary>The documented conformance value encoding, decoded into a CLR value for the target type.</summary>
    private static object? ConvertTo(JsonElement value, Type targetType)
    {
        if (value.ValueKind == JsonValueKind.Null)
        {
            return null;
        }

        var target = Nullable.GetUnderlyingType(targetType) ?? targetType;
        if (value.ValueKind == JsonValueKind.Number)
        {
            return Convert.ChangeType(
                value.TryGetInt64(out var l) ? l : value.GetDouble(), target, CultureInfo.InvariantCulture);
        }

        if (value.ValueKind is JsonValueKind.True or JsonValueKind.False)
        {
            return value.GetBoolean();
        }

        var text = value.GetString()!;
        if (target.IsEnum)
        {
            return Enum.Parse(target, text, ignoreCase: true);
        }

        if (target == typeof(DateTime))
        {
            return DateTimeOffset.Parse(text, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind).UtcDateTime;
        }

        if (target == typeof(decimal))
        {
            return decimal.Parse(text, CultureInfo.InvariantCulture);
        }

        if (target == typeof(Guid))
        {
            return Guid.Parse(text);
        }

        return text;
    }

    /// <summary>The documented conformance value encoding, reduced to string form for comparison.</summary>
    private static string? Encode(object? value) => value switch
    {
        null => null,
        bool b => b ? "true" : "false",
        short or int or long => Convert.ToInt64(value, CultureInfo.InvariantCulture).ToString(CultureInfo.InvariantCulture),
        decimal m => m.ToString("0.############################", CultureInfo.InvariantCulture),   // scale-normalized
        float or double => Convert.ToDouble(value, CultureInfo.InvariantCulture).ToString(CultureInfo.InvariantCulture),
        DateTime dt => dt.ToString("o", CultureInfo.InvariantCulture),
        Enum e => e.ToString(),
        _ => value.ToString(),
    };

    private static async Task InvokeAsync(Db db, string method, Type entityType, object argument)
    {
        try
        {
            await (Task)typeof(Db).GetMethods()
                .Single(m => m.Name == method && m.GetGenericArguments().Length == 1 && m.GetParameters().Length == 2)
                .MakeGenericMethod(entityType)
                .Invoke(db, [argument, CancellationToken.None])!;
        }
        catch (TargetInvocationException exception) when (exception.InnerException is not null)
        {
            throw exception.InnerException;
        }
    }

    private static async Task<object?> InvokeWithResultAsync(Db db, string method, Type entityType, object argument)
    {
        try
        {
            var task = (Task)typeof(Db).GetMethods()
                .Single(m => m.Name == method && m.GetGenericArguments().Length == 1 && m.GetParameters().Length == 2)
                .MakeGenericMethod(entityType)
                .Invoke(db, [argument, CancellationToken.None])!;
            await task.ConfigureAwait(false);
            return task.GetType().GetProperty("Result")!.GetValue(task);
        }
        catch (TargetInvocationException exception) when (exception.InnerException is not null)
        {
            throw exception.InnerException;
        }
    }

    private static string ConformanceDirectory()
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            var candidate = Path.Combine(dir.FullName, "conformance");
            if (Directory.Exists(candidate))
            {
                return candidate;
            }
        }

        throw new InvalidOperationException("conformance/ directory not found above the test output directory");
    }
}
