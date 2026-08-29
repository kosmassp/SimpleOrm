using System.Globalization;
using System.Text.Json;
using System.Text.Json.Nodes;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;

namespace SimpleOrm.Tests;

/// <summary>
/// Shared plumbing for the case-style conformance runners: the fixture database
/// (metadata-created tables + conformance/fixtures/seed.json) and the documented
/// value encoding (spec/mapping-rules.md).
/// </summary>
internal static class ConformanceDatabase
{
    public static readonly Dictionary<string, Type> EntityTypes = new()
    {
        ["User"] = typeof(User),
        ["Role"] = typeof(Role),
        ["UserRole"] = typeof(UserRole),
        ["UserProfile"] = typeof(UserProfile),
        ["Transaction"] = typeof(Transaction),
        ["TransactionDetail"] = typeof(TransactionDetail),
        ["UserTransactionTotal"] = typeof(UserTransactionTotal),
    };

    public static async Task BuildAsync(string databasePath, string conformanceDirectory)
    {
        await using (var db = await Db.OpenAsync(
            $"Data Source={databasePath}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None))
        {
            await db.CreateTableAsync<User>(CancellationToken.None);
            await db.CreateTableAsync<Role>(CancellationToken.None);
            await db.CreateTableAsync<UserRole>(CancellationToken.None);
            await db.CreateTableAsync<UserProfile>(CancellationToken.None);
            await db.CreateTableAsync<Transaction>(CancellationToken.None);
            await db.CreateTableAsync<TransactionDetail>(CancellationToken.None);
            await db.CreateViewAsync<UserTransactionTotal>(CancellationToken.None);
        }

        using var document = JsonDocument.Parse(
            File.ReadAllText(Path.Combine(conformanceDirectory, "fixtures", "seed.json")));
        using var connection = new SqliteConnection($"Data Source={databasePath}");
        connection.Open();
        foreach (var table in document.RootElement.EnumerateObject())
        {
            foreach (var row in table.Value.EnumerateArray())
            {
                var columns = row.EnumerateObject().Select(p => p.Name).ToArray();
                using var command = connection.CreateCommand();
                command.CommandText = "insert into " + table.Name
                    + " (" + string.Join(", ", columns) + ") values ("
                    + string.Join(", ", columns.Select(c => "@" + c)) + ")";
                foreach (var property in row.EnumerateObject())
                {
                    command.Parameters.AddWithValue("@" + property.Name, property.Value.ValueKind switch
                    {
                        JsonValueKind.Null => DBNull.Value,
                        JsonValueKind.Number => property.Value.TryGetInt64(out var i) ? i : property.Value.GetDouble(),
                        JsonValueKind.True => 1L,
                        JsonValueKind.False => 0L,
                        _ => property.Value.GetString()!,
                    });
                }

                command.ExecuteNonQuery();
            }
        }
    }

    /// <summary>The documented conformance value encoding (spec/mapping-rules.md).</summary>
    public static JsonNode? Encode(object? value) => value switch
    {
        null => null,
        bool b => JsonValue.Create(b),
        short s => JsonValue.Create((long)s),
        int i => JsonValue.Create((long)i),
        long l => JsonValue.Create(l),
        float f => JsonValue.Create((double)f),
        double d => JsonValue.Create(d),
        decimal m => JsonValue.Create(m.ToString(CultureInfo.InvariantCulture)),
        string s => JsonValue.Create(s),
        DateTime dt => JsonValue.Create(dt.ToString("o", CultureInfo.InvariantCulture)),
        DateTimeOffset dto => JsonValue.Create(dto.ToString("o", CultureInfo.InvariantCulture)),
        DateOnly d => JsonValue.Create(d.ToString("O", CultureInfo.InvariantCulture)),
        TimeOnly t => JsonValue.Create(t.ToString("O", CultureInfo.InvariantCulture)),
        Guid g => JsonValue.Create(g.ToString("D").ToLowerInvariant()),
        byte[] bytes => JsonValue.Create(Convert.ToBase64String(bytes)),
        Enum e => JsonValue.Create(e.ToString()),
        _ => JsonValue.Create(value.ToString()),
    };

    /// <summary>An entity's mapped columns as an encoded JSON object.</summary>
    public static JsonObject EncodeEntity(EntityMap map, object entity)
    {
        var row = new JsonObject();
        foreach (var property in map.Properties)
        {
            row[property.ColumnName] = Encode(property.Property.GetValue(entity));
        }

        return row;
    }
}
