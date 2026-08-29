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

    [Fact]
    public void Ddl_snapshot_round_trips_and_normalizes()
    {
        var json = SchemaSnapshot.ExportDdl(
            "totals", "view", "create view if not exists totals as\n  select 1 as one",
            asOfVersion: 6, DateTimeOffset.UtcNow);

        var (objectName, kind, ddl, asOfVersion) = SchemaSnapshot.ParseDdl(json);
        Assert.Equal(("totals", "view", 6L), (objectName, kind, asOfVersion));
        Assert.Equal("create view totals as select 1 as one", ddl);
    }

    [Fact]
    public void Ddl_normalization_canonicalizes_the_prefix_the_database_rewrites()
    {
        // SQLite stores "CREATE VIEW x" for "create view if not exists x": layout,
        // case, and IF NOT EXISTS in the prefix are not schema; the body is.
        var rendered = SchemaSnapshot.NormalizeDdl("create view if not exists totals as\n    select 1 as one");
        var introspected = SchemaSnapshot.NormalizeDdl("CREATE VIEW totals as select 1 as one");
        Assert.Equal(rendered, introspected);

        Assert.NotEqual(
            SchemaSnapshot.NormalizeDdl("create view totals as select 1 as one"),
            SchemaSnapshot.NormalizeDdl("create view totals as select 2 as one"));
    }
}
