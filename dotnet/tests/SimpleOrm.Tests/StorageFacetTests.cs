using SimpleOrm.Postgres;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Storage facets (ADR-0043, metadata-model.md "Storage facets"): dialect-neutral
/// size declarations on <c>[Column]</c> — <c>Length</c> for strings and bytes,
/// <c>Precision</c>/<c>Scale</c> for decimals — exported when declared, rendered
/// by each dialect its own way (SQLite ignores them), and <c>MAP-032</c> wherever
/// misplaced. Without them a real tree's <c>decimal(18,2)</c> would be a
/// <c>DDL-004</c> against the model's default on every <c>diff</c>.
/// </summary>
public sealed class StorageFacetTests
{
    private static readonly EntityMapLoader Loader = new();

    [Fact]
    public void Facets_load_from_the_attribute_and_export_after_renamedFrom()
    {
        var map = Loader.Load<Sized>();
        var code = map.Properties.Single(p => p.PropertyName == nameof(Sized.Code));
        var price = map.Properties.Single(p => p.PropertyName == nameof(Sized.Price));
        var whole = map.Properties.Single(p => p.PropertyName == nameof(Sized.Whole));
        Assert.Equal((50, (int?)null, (int?)null), (code.Length, code.Precision, code.Scale));
        Assert.Equal(((int?)null, 18, 2), (price.Length, price.Precision, price.Scale));
        Assert.Equal(((int?)null, 10, 0), (whole.Length, whole.Precision, whole.Scale));   // Scale omitted reads 0 beside a Precision

        var json = EntityMapJson.Export(map, Loader);
        Assert.Contains("\"column\": \"code\",\n      \"type\": \"string\",\n      \"nullable\": false,\n      \"length\": 50", json.Replace("\r", string.Empty));
        Assert.Contains("\"precision\": 18,\n      \"scale\": 2", json.Replace("\r", string.Empty));
        Assert.Contains("\"precision\": 10,\n      \"scale\": 0", json.Replace("\r", string.Empty));

        var fixture = EntityMapJson.Export(Loader.Load<Transaction>(), Loader);
        Assert.Contains("\"precision\": 18", fixture);
    }

    [Fact]
    public void Each_dialect_renders_the_facets_its_own_way_and_sqlite_ignores_them()
    {
        var map = Loader.Load<Sized>();
        string Type(IDialect dialect, string property) => dialect.StorageType(map, map.Properties.Single(p => p.PropertyName == property));

        var sqlServer = new SqlServerDialect();
        Assert.Equal("nvarchar(50)", Type(sqlServer, nameof(Sized.Code)));
        Assert.Equal("varbinary(16)", Type(sqlServer, nameof(Sized.Hash)));
        Assert.Equal("decimal(18,2)", Type(sqlServer, nameof(Sized.Price)));
        Assert.Equal("decimal(10,0)", Type(sqlServer, nameof(Sized.Whole)));
        Assert.Equal("decimal(38,9)", Type(sqlServer, nameof(Sized.Plain)));   // the default, spelled as the columns query reports it
        Assert.Equal("nvarchar(320)", Type(sqlServer, nameof(Sized.Email)));   // a declared length wins over the indexable 450

        var postgres = new PostgresDialect();
        Assert.Equal("character varying(50)", Type(postgres, nameof(Sized.Code)));
        Assert.Equal("bytea", Type(postgres, nameof(Sized.Hash)));   // bytea has no length
        Assert.Equal("numeric(18,2)", Type(postgres, nameof(Sized.Price)));
        Assert.Equal("numeric", Type(postgres, nameof(Sized.Plain)));

        var sqlite = new SqliteDialect();
        Assert.Equal("TEXT", Type(sqlite, nameof(Sized.Code)));
        Assert.Equal("TEXT", Type(sqlite, nameof(Sized.Price)));
        Assert.Equal("BLOB", Type(sqlite, nameof(Sized.Hash)));
    }

    [Fact]
    public void The_indexable_length_is_the_storage_type_rule_now()
    {
        // ADR-0043 retires Clarification 11: snapshots, diff, and sync see what CREATE TABLE
        // emits and introspection reports — an indexed string column is nvarchar(450).
        var users = Loader.Load<User>();
        var dialect = new SqlServerDialect();
        Assert.Equal("nvarchar(450)", dialect.StorageType(users, users.Properties.Single(p => p.PropertyName == nameof(User.Email))));
        Assert.Equal("nvarchar(450)", dialect.StorageType(users, users.Properties.Single(p => p.PropertyName == nameof(User.DisplayName))));
        Assert.Equal("nvarchar(max)", dialect.StorageType(users, users.Properties.Single(p => p.PropertyName == nameof(User.Name))));
        Assert.Contains("[email] nvarchar(450) not null", dialect.CreateTableSql(users));
    }

    [Fact]
    public void Misplaced_facets_are_MAP_032()
    {
        foreach (var type in new[] { typeof(LengthOnInt), typeof(PrecisionOnString), typeof(ScaleAlone), typeof(ScaleAbovePrecision), typeof(ZeroLength), typeof(FacetOnView) })
        {
            var errors = Assert.Throws<MappingException>(() => Loader.Load(type)).Errors;
            Assert.Contains(errors, e => e.Code == "MAP-032");
        }
    }

    [Fact]
    public void The_builder_declares_facets_under_the_same_rules()
    {
        var builder = new EntityMapBuilder<Plain>();
        builder.ToTable("plain");
        builder.Property(x => x.Id).Key().Generated();
        builder.Property(x => x.Code).Length(12);
        builder.Property(x => x.Price).Precision(9, 3);
        var map = new EntityMapLoader(new MappingOptions().Register(builder)).Load<Plain>();
        Assert.Equal(12, map.Properties.Single(p => p.PropertyName == nameof(Plain.Code)).Length);
        Assert.Equal((9, 3), (map.Properties.Single(p => p.PropertyName == nameof(Plain.Price)).Precision, map.Properties.Single(p => p.PropertyName == nameof(Plain.Price)).Scale));

        var bad = new EntityMapBuilder<Plain>();
        bad.ToTable("plain");
        bad.Property(x => x.Id).Key().Generated();
        bad.Property(x => x.Code).Precision(9, 3);   // a precision on a string
        bad.Property(x => x.Price);
        Assert.Contains(Assert.Throws<MappingException>(() => new EntityMapLoader(new MappingOptions().Register(bad)).Load<Plain>()).Errors, e => e.Code == "MAP-032");
    }

    [Table("sized")]
    [Index(nameof(Email))]
    public sealed class Sized
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column(Length = 50)]
        public required string Code { get; set; }

        [Column(Length = 320)]
        public required string Email { get; set; }

        [Column(Length = 16)]
        public byte[]? Hash { get; set; }

        [Column(Precision = 18, Scale = 2)]
        public decimal Price { get; set; }

        [Column(Precision = 10)]
        public decimal Whole { get; set; }

        [Column]
        public decimal Plain { get; set; }
    }

    public sealed class Plain
    {
        public long Id { get; set; }

        public string? Code { get; set; }

        public decimal Price { get; set; }
    }

    [Table("bad_length")]
    public sealed class LengthOnInt
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column(Length = 5)]
        public int Count { get; set; }
    }

    [Table("bad_precision")]
    public sealed class PrecisionOnString
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column(Precision = 5)]
        public string? Name { get; set; }
    }

    [Table("bad_scale")]
    public sealed class ScaleAlone
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column(Scale = 2)]
        public decimal Price { get; set; }
    }

    [Table("bad_scale_above")]
    public sealed class ScaleAbovePrecision
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column(Precision = 4, Scale = 6)]
        public decimal Price { get; set; }
    }

    [Table("bad_zero")]
    public sealed class ZeroLength
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column(Length = -1)]
        public string? Name { get; set; }
    }

    [View("facet_view", "select 1 as id, 'x' as name")]
    public sealed class FacetOnView
    {
        [Column]
        public long Id { get; set; }

        [Column(Length = 10)]
        public string? Name { get; set; }
    }
}
