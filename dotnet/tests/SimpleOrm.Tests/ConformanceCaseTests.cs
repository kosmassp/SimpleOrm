using System.Collections;
using System.Globalization;
using System.Reflection;
using System.Text.Json;
using System.Text.Json.Nodes;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The conformance-case runner (§9): each conformance/cases/*.json runs against a
/// fresh database built from entity metadata and seeded from
/// conformance/fixtures/seed.json; results compare via the value encoding
/// documented in spec/mapping-rules.md, errors compare by code.
/// </summary>
public sealed class ConformanceCaseTests
{
    public static TheoryData<string> CaseFiles
    {
        get
        {
            var data = new TheoryData<string>();
            foreach (var file in Directory.GetFiles(Path.Combine(ConformanceDirectory(), "cases"), "*.json"))
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
            File.ReadAllText(Path.Combine(ConformanceDirectory(), "cases", fileName)));
        var spec = document.RootElement;
        var query = spec.GetProperty("query").GetString()!;
        var result = spec.GetProperty("result").GetString()!;
        var expect = spec.GetProperty("expect");

        var databasePath = Path.Combine(Path.GetTempPath(), $"simpleorm_case_{Guid.NewGuid():N}.db");
        try
        {
            await ConformanceDatabase.BuildAsync(databasePath, ConformanceDirectory());

            if (expect.TryGetProperty("error", out var errorCode))
            {
                var thrown = await RunExpectingErrorAsync(databasePath, result, query);
                Assert.Equal(errorCode.GetString(), thrown);
            }
            else
            {
                var actual = result == "raw"
                    ? RunRaw(databasePath, query)
                    : await RunEntityAsync(databasePath, result, query);
                var expected = JsonNode.Parse(expect.GetProperty("rows").GetRawText());
                Assert.True(
                    JsonNode.DeepEquals(expected, actual),
                    $"expected {expected!.ToJsonString()}\nactual   {actual.ToJsonString()}");
            }
        }
        finally
        {
            SqliteConnection.ClearAllPools();
            File.Delete(databasePath);
        }
    }

    private static JsonArray RunRaw(string databasePath, string query)
    {
        using var connection = new SqliteConnection($"Data Source={databasePath}");
        connection.Open();
        using var command = connection.CreateCommand();
        command.CommandText = query;
        using var reader = command.ExecuteReader();

        var rows = new JsonArray();
        while (reader.Read())
        {
            var row = new JsonObject();
            for (var i = 0; i < reader.FieldCount; i++)
            {
                row[reader.GetName(i)] = reader.GetValue(i) switch
                {
                    DBNull => null,
                    long l => JsonValue.Create(l),
                    double d => JsonValue.Create(d),
                    byte[] bytes => JsonValue.Create(Convert.ToBase64String(bytes)),
                    var other => JsonValue.Create(other.ToString()),
                };
            }

            rows.Add(row);
        }

        return rows;
    }

    private static async Task<JsonArray> RunEntityAsync(string databasePath, string entityName, string query)
    {
        var (rows, map) = await ExecuteEntityQueryAsync(databasePath, entityName, query);
        var encoded = new JsonArray();
        foreach (var entity in rows)
        {
            encoded.Add(ConformanceDatabase.EncodeEntity(map, entity));
        }

        return encoded;
    }

    private static async Task<string> RunExpectingErrorAsync(string databasePath, string entityName, string query)
    {
        try
        {
            await ExecuteEntityQueryAsync(databasePath, entityName, query);
            return "(no error)";
        }
        catch (SimpleOrmException exception)
        {
            return exception.Code;
        }
        catch (MappingException exception)
        {
            return exception.Errors[0].Code;
        }
    }

    private static async Task<(IEnumerable Rows, EntityMap Map)> ExecuteEntityQueryAsync(
        string databasePath, string entityName, string query)
    {
        var entityType = ConformanceDatabase.EntityTypes[entityName];
        await using var db = await Db.OpenAsync(
            $"Data Source={databasePath}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);

        var source = Query.Inline(query);
        var queryType = typeof(Query<,>).MakeGenericType(typeof(EmptyArgs), entityType);
        var typedQuery = queryType.GetMethod("op_Implicit")!.Invoke(null, [source])!;
        var method = typeof(Db).GetMethods()
            .Single(m => m.Name == nameof(Db.QueryAsync) && m.GetGenericArguments().Length == 2)
            .MakeGenericMethod(typeof(EmptyArgs), entityType);

        try
        {
            var task = (Task)method.Invoke(db, [typedQuery, EmptyArgs.Value, CancellationToken.None])!;
            await task.ConfigureAwait(false);
            var rows = (IEnumerable)task.GetType().GetProperty("Result")!.GetValue(task)!;
            return (rows, db.Maps.Load(entityType));
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
