using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

public sealed class SchemaSnapshotTests
{
    [Fact]
    public void Snapshot_carries_version_time_columns_and_indexes()
    {
        var map = new EntityMapLoader().Load<User>();
        var json = SchemaSnapshot.Export(
            map, new SqliteDialect(), asOfVersion: 2, new DateTimeOffset(2026, 8, 29, 12, 0, 0, TimeSpan.Zero));

        Assert.Contains("\"object\": \"users\"", json);
        Assert.Contains("\"asOfVersion\": 2", json);
        Assert.Contains("\"generatedAt\": \"2026-08-29T12:00:00.0000000Z\"", json);
        Assert.Contains("\"column\": \"display_name\"", json);
        Assert.Contains("\"type\": \"TEXT\"", json);
        Assert.Contains("\"name\": \"ix_users_email\"", json);
    }

    [Fact]
    public void Snapshot_round_trips_through_parse()
    {
        var map = new EntityMapLoader().Load<User>();
        var dialect = new SqliteDialect();
        var json = SchemaSnapshot.Export(map, dialect, asOfVersion: 7, DateTimeOffset.UtcNow);

        var (schema, asOfVersion) = SchemaSnapshot.Parse(json);
        Assert.Equal(7, asOfVersion);
        Assert.Equal("users", schema.Name);
        Assert.Equal(map.Properties.Count, schema.Columns.Count);

        var id = Assert.Single(schema.Columns, c => c.Name == "id");
        Assert.True(id.Key);
        Assert.True(id.Generated);
        Assert.Equal("INTEGER", id.StorageType);

        // Name-sorted for determinism: introspection order and metadata order both normalize.
        Assert.Equal(schema.Columns.Select(c => c.Name).OrderBy(n => n, StringComparer.Ordinal), schema.Columns.Select(c => c.Name));
    }
}
