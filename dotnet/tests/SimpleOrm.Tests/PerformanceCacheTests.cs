using SimpleOrm.Postgres;
using SimpleOrm.SqlServer;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The performance pass's caches (ADR-0048): a cached rendering is the dialect's
/// own text, one entry per (map, dialect) instance pair, and the rule that lets
/// loading skip its post-fetch sort holds only where the engine orders the key
/// by value.
/// </summary>
public sealed class PerformanceCacheTests
{
    [Table("perf_long_keys")]
    private sealed class LongKeyed
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string Name { get; set; } = string.Empty;

        [Column]
        [Version]
        public long Version { get; set; }
    }

    [Table("perf_decimal_keys")]
    private sealed class DecimalKeyed
    {
        [Key]
        [Column]
        public decimal Id { get; set; }
    }

    [Table("perf_string_keys")]
    private sealed class StringKeyed
    {
        [Key]
        [Column]
        public string Code { get; set; } = string.Empty;
    }

    [Fact]
    public void A_cached_rendering_is_the_dialects_own_text_per_map_and_dialect()
    {
        var map = new EntityMapLoader().Load<LongKeyed>();
        foreach (IDialect dialect in new IDialect[] { new SqliteDialect(), new SqlServerDialect(), new PostgresDialect() })
        {
            var generated = GeneratedSql.For(map, dialect);
            Assert.Same(generated, GeneratedSql.For(map, dialect));
            Assert.Equal(dialect.InsertSql(map), generated.Insert);
            Assert.Equal(dialect.UpdateSql(map), generated.Update);
            Assert.Equal(dialect.DeleteSql(map, checkVersion: false), generated.Delete(false));
            Assert.Equal(dialect.DeleteSql(map, checkVersion: true), generated.Delete(true));
            var set = new[] { map.Properties.Single(p => p.PropertyName == nameof(LongKeyed.Name)) };
            Assert.Equal(dialect.UpdateOnlySql(map, set), generated.UpdateOnly(set));
            Assert.Same(generated.UpdateOnly(set), generated.UpdateOnly([set[0]]));   // keyed by the ordered set, not the list instance
        }

        // Another dialect instance, even of the same type, is another entry.
        Assert.NotSame(GeneratedSql.For(map, new SqliteDialect()), GeneratedSql.For(map, new SqliteDialect()));
    }

    [Fact]
    public void The_engine_orders_by_value_only_for_numeric_keys_it_stores_as_numbers()
    {
        var maps = new EntityMapLoader();
        var longKeyed = maps.Load<LongKeyed>();
        var decimalKeyed = maps.Load<DecimalKeyed>();
        var stringKeyed = maps.Load<StringKeyed>();

        Assert.True(GeneratedSql.For(longKeyed, new SqliteDialect()).KeyOrderIsValueOrder);
        Assert.True(GeneratedSql.For(longKeyed, new PostgresDialect()).KeyOrderIsValueOrder);
        Assert.True(GeneratedSql.For(longKeyed, new SqlServerDialect()).KeyOrderIsValueOrder);

        Assert.False(GeneratedSql.For(decimalKeyed, new SqliteDialect()).KeyOrderIsValueOrder);   // TEXT: '10' before '2'
        Assert.True(GeneratedSql.For(decimalKeyed, new PostgresDialect()).KeyOrderIsValueOrder);
        Assert.True(GeneratedSql.For(decimalKeyed, new SqlServerDialect()).KeyOrderIsValueOrder);

        Assert.False(GeneratedSql.For(stringKeyed, new SqliteDialect()).KeyOrderIsValueOrder);
        Assert.False(GeneratedSql.For(stringKeyed, new PostgresDialect()).KeyOrderIsValueOrder);   // collations are not ordinal
    }
}
