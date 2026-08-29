using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

public sealed class SchemaSnapshotTests
{
    [Fact]
    public void Snapshot_carries_version_time_columns_and_indexes()
    {
        var map = new EntityMapLoader().Load<User>();
        var json = SchemaSnapshot.Export(
            map, asOfVersion: 2, new DateTimeOffset(2026, 8, 29, 12, 0, 0, TimeSpan.Zero));

        Assert.Contains("\"object\": \"users\"", json);
        Assert.Contains("\"asOfVersion\": 2", json);
        Assert.Contains("\"generatedAt\": \"2026-08-29T12:00:00.0000000Z\"", json);
        Assert.Contains("\"column\": \"display_name\"", json);
        Assert.Contains("\"name\": \"ix_users_email\"", json);
    }
}
