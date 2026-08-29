using System.Reflection;
using System.Text.Json;
using System.Text.Json.Nodes;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The load-case runner (§9, ADR-0021): each conformance/load-cases/*.json loads
/// one declared navigation for a set of owner keys against the seeded fixture
/// database and checks the loaded values per owner — an object (or null) for
/// singular navigations, an array ordered by target key for collections. Values
/// are keyed by column name in the conformance value encoding; listed columns are
/// checked, others ignored; array lengths must match exactly.
/// </summary>
public sealed class ConformanceLoadTests
{
    public static TheoryData<string> CaseFiles
    {
        get
        {
            var data = new TheoryData<string>();
            foreach (var file in Directory.GetFiles(
                Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "load-cases"), "*.json"))
            {
                data.Add(Path.GetFileName(file));
            }

            return data;
        }
    }

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public async Task Load_behaves_as_specified(string fileName)
    {
        using var document = JsonDocument.Parse(File.ReadAllText(
            Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "load-cases", fileName)));
        var spec = document.RootElement;
        var load = spec.GetProperty("load");
        var entityType = ConformanceDatabase.EntityTypes[load.GetProperty("entity").GetString()!];
        var navigation = load.GetProperty("navigation").GetString()!;

        // A key is an integer, or an array of parts for composite-key owners;
        // in `loaded`, composite keys join their parts with '|'.
        var keys = load.GetProperty("keys").EnumerateArray()
            .Select(k => k.ValueKind == JsonValueKind.Array
                ? (Token: string.Join("|", k.EnumerateArray().Select(p => p.GetInt64())),
                   Value: BoxedKey(k.EnumerateArray().Select(p => p.GetInt64()).ToArray()))
                : (Token: k.GetInt64().ToString(), Value: (object)k.GetInt64()))
            .ToArray();
        var expect = spec.GetProperty("expect");

        var databasePath = Path.Combine(Path.GetTempPath(), $"simpleorm_load_{Guid.NewGuid():N}.db");
        try
        {
            await ConformanceDatabase.BuildAsync(
                databasePath, ConformanceMigrationTests.ConformanceDirectory());
            await using var db = await Db.OpenAsync(
                $"Data Source={databasePath}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);

            var entities = new List<object>();
            foreach (var key in keys)
            {
                entities.Add(await GetAsync(db, entityType, key.Value));
            }

            string? error = null;
            try
            {
                await LoadEachAsync(db, entityType, entities, navigation);
            }
            catch (SimpleOrmException exception)
            {
                error = exception.Code;
            }

            if (expect.TryGetProperty("error", out var expectedError))
            {
                Assert.Equal(expectedError.GetString(), error);
                return;
            }

            Assert.Null(error);
            var navigationProperty = entityType.GetProperty(navigation)!;
            var loadedExpectations = expect.GetProperty("loaded");
            for (var i = 0; i < keys.Length; i++)
            {
                var expected = loadedExpectations.GetProperty(keys[i].Token);
                var actual = navigationProperty.GetValue(entities[i]);
                AssertLoaded(db, expected, actual, $"key {keys[i].Token}");
            }
        }
        finally
        {
            SqliteConnection.ClearAllPools();
            File.Delete(databasePath);
        }
    }

    private static void AssertLoaded(Db db, JsonElement expected, object? actual, string context)
    {
        switch (expected.ValueKind)
        {
            case JsonValueKind.Null:
                Assert.True(actual is null, $"{context}: expected null, got a loaded instance");
                break;
            case JsonValueKind.Array:
                var rows = ((System.Collections.IEnumerable)actual!).Cast<object>().ToArray();
                var expectedRows = expected.EnumerateArray().ToArray();
                Assert.Equal(expectedRows.Length, rows.Length);
                for (var i = 0; i < rows.Length; i++)
                {
                    AssertValues(db, expectedRows[i], rows[i], $"{context}[{i}]");
                }

                break;
            default:
                Assert.True(actual is not null, $"{context}: expected a loaded instance, got null");
                AssertValues(db, expected, actual!, context);
                break;
        }
    }

    private static void AssertValues(Db db, JsonElement expected, object entity, string context)
    {
        var encoded = ConformanceDatabase.EncodeEntity(db.Maps.Load(entity.GetType()), entity);
        foreach (var property in expected.EnumerateObject())
        {
            var actual = encoded[property.Name];
            var expectedNode = JsonNode.Parse(property.Value.GetRawText());
            Assert.True(
                JsonNode.DeepEquals(expectedNode, actual),
                $"{context}.{property.Name}: expected {property.Value.GetRawText()}, got {actual?.ToJsonString() ?? "null"}");
        }
    }

    private static object BoxedKey(long[] parts) => parts.Length switch
    {
        2 => (parts[0], parts[1]),
        3 => (parts[0], parts[1], parts[2]),
        _ => throw new InvalidOperationException($"composite case keys support 2-3 parts, got {parts.Length}"),
    };

    private static async Task<object> GetAsync(Db db, Type entityType, object key)
    {
        var method = typeof(Db).GetMethods()
            .Single(m => m.Name == nameof(Db.GetAsync) && m.GetParameters().Length == 2)
            .MakeGenericMethod(entityType);
        var task = (Task)method.Invoke(db, [key, CancellationToken.None])!;
        await task.ConfigureAwait(false);
        return task.GetType().GetProperty("Result")!.GetValue(task)!;
    }

    private static async Task LoadEachAsync(Db db, Type entityType, IReadOnlyList<object> entities, string navigation)
    {
        var typed = Array.CreateInstance(entityType, entities.Count);
        for (var i = 0; i < entities.Count; i++)
        {
            typed.SetValue(entities[i], i);
        }

        var method = typeof(Db).GetMethod(nameof(Db.LoadEachAsync))!.MakeGenericMethod(entityType);
        try
        {
            await ((Task)method.Invoke(db, [typed, navigation, CancellationToken.None])!).ConfigureAwait(false);
        }
        catch (TargetInvocationException exception) when (exception.InnerException is not null)
        {
            throw exception.InnerException;
        }
    }
}
