using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Owned value types (ADR-0030): a class-level [Owned] type whose [Column] members
/// flatten into the owner's table under a prefix; read back as one instance (or
/// null when every member column is NULL and the navigation is nullable); written,
/// filtered, and column-listed through dotted property paths.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class OwnedTypesTests(SqliteFixture fixture)
{
    private static UserProfile NewProfile(long userId, Address? address) => new()
    {
        UserId = userId,
        Bio = "bio",
        Address = address,
        CreatedAtUtc = TestDb.SeedTime,
    };

    [Fact]
    public void Loader_flattens_members_under_the_navigation_prefix()
    {
        var map = new EntityMapLoader().Load<UserProfile>();

        var owned = Assert.Single(map.OwnedTypes);
        Assert.Equal("Address", owned.PropertyName);
        Assert.Equal("address_", owned.Prefix);
        Assert.True(owned.IsNullable);
        Assert.Equal(["Address.Street", "Address.City", "Address.PostalCode"], owned.Members.Select(m => m.PropertyName));
        Assert.Equal(["address_street", "address_city", "address_postal_code"], owned.Members.Select(m => m.ColumnName));
        Assert.All(owned.Members, m => Assert.True(m.IsNullable));                 // a nullable navigation makes every member nullable
        Assert.All(owned.Members, m => Assert.Contains(m, map.Properties));         // the same instances appear in the owner's list
        Assert.Equal(typeof(string), map.Properties.Single(p => p.PropertyName == "Address.Street").ClrType);
    }

    [Fact]
    public async Task Round_trips_an_address_and_reads_an_all_null_segment_as_no_address()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        var grace = await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");

        var withAddress = NewProfile(ada.Id, new Address { Street = "1 Main St", City = "Paris" });
        await db.InsertAsync(withAddress, CancellationToken.None);
        var without = NewProfile(grace.Id, null);
        await db.InsertAsync(without, CancellationToken.None);

        var loaded = await db.GetAsync<UserProfile>(withAddress.Id, CancellationToken.None);
        Assert.NotNull(loaded.Address);
        Assert.Equal("1 Main St", loaded.Address.Street);
        Assert.Equal("Paris", loaded.Address.City);
        Assert.Null(loaded.Address.PostalCode);

        var none = await db.GetAsync<UserProfile>(without.Id, CancellationToken.None);
        Assert.Null(none.Address);                                                   // all three columns NULL → no instance
    }

    [Fact]
    public async Task Criteria_and_column_lists_use_the_dotted_path_and_the_navigation_name()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        var profile = NewProfile(ada.Id, new Address { Street = "1 Main St", City = "Paris" });
        await db.InsertAsync(profile, CancellationToken.None);

        var found = await db.Query<UserProfile>()
            .Where(Criteria.Eq("Address.City", "Paris"))
            .OrderBy("Address.Street")
            .ToListAsync(CancellationToken.None);
        Assert.Equal(profile.Id, Assert.Single(found).Id);

        profile.Address!.City = "Lyon";
        profile.Address.PostalCode = "69001";
        profile.Bio = "not written";
        await db.UpdateOnlyAsync(profile, ["Address"], CancellationToken.None);      // the navigation name expands to its members

        var loaded = await db.GetAsync<UserProfile>(profile.Id, CancellationToken.None);
        Assert.Equal("Lyon", loaded.Address!.City);
        Assert.Equal("69001", loaded.Address.PostalCode);
        Assert.Equal("bio", loaded.Bio);

        var repeated = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.UpdateOnlyAsync(profile, ["Address", "Address.City"], CancellationToken.None));
        Assert.Equal("CRUD-007", repeated.Code);

        var unknown = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.Query<UserProfile>().Where(Criteria.Eq("Address.Nope", "x")).ToListAsync(CancellationToken.None));
        Assert.Equal("QRY-006", unknown.Code);
    }

    [Fact]
    public async Task Ddl_from_metadata_and_export_carry_the_flattened_columns()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await db.CreateTableAsync<Parcel>(CancellationToken.None);

        var parcel = new Parcel { Label = "box", Origin = new Point { X = 1, Y = 2 }, Destination = new Point { X = 3, Y = 4 } };
        await db.InsertAsync(parcel, CancellationToken.None);
        var loaded = await db.GetAsync<Parcel>(parcel.Id, CancellationToken.None);
        Assert.Equal((1, 2), (loaded.Origin.X, loaded.Origin.Y));
        Assert.Equal((3, 4), (loaded.Destination.X, loaded.Destination.Y));

        var map = db.Maps.Load<Parcel>();
        Assert.Equal(["id", "label", "from_x", "from_y", "x", "y"], map.Properties.Select(p => p.ColumnName));
        Assert.False(map.Properties.Single(p => p.PropertyName == "Origin.X").IsNullable);   // a required navigation keeps member nullability

        var json = EntityMapJson.Export(map, db.Maps);
        Assert.Contains("\"column\": \"from_x\"", json);
        Assert.DoesNotContain("Origin", json);                                       // the export is column-centric: no owned structure
    }

    [Fact]
    public void Loader_refuses_invalid_owned_declarations()
    {
        static IEnumerable<string> CodesOf<T>() where T : class
            => Assert.Throws<MappingException>(() => new EntityMapLoader().Load<T>()).Errors.Select(e => e.Code);

        Assert.Contains("MAP-024", CodesOf<OwnsAnEntity>());          // the owned type carries [Table]
        Assert.Contains("MAP-024", CodesOf<OwnsUndeclared>());        // the owned type lacks the class-level [Owned]
        Assert.Contains("MAP-024", CodesOf<OwnsAKeyedType>());        // a [Key] inside the owned type
        Assert.Contains("MAP-024", CodesOf<OwnsANestedOwned>());      // nested [Owned]
        Assert.Contains("MAP-024", CodesOf<OwnsACollection>());       // a collection navigation
        Assert.Contains("MAP-024", CodesOf<OwnsNothingMapped>());     // no [Column] member
        Assert.Contains("MAP-019", CodesOf<OwnedAndColumn>());        // [Owned] combined with [Column]
        Assert.Contains("MAP-018", CodesOf<PrefixCollision>());       // an owned column collides with a direct one
        Assert.Contains("MAP-024", CodesOf<Point>());                 // an owned type loaded as if it were an entity
    }

    // --- fixtures -------------------------------------------------------------------

    [Owned]
    public sealed class Point
    {
        [Column]
        public int X { get; set; }

        [Column]
        public int Y { get; set; }
    }

    [Table("parcels")]
    public sealed class Parcel
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public required string Label { get; set; }

        [Owned(Prefix = "from_")]
        public required Point Origin { get; set; }

        [Owned(Prefix = "")]
        public required Point Destination { get; set; }
    }

    [Table("t1")]
    public sealed class OwnsAnEntity
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Owned]
        public Role? Role { get; set; }
    }

    public sealed class Undeclared
    {
        [Column]
        public string? Name { get; set; }
    }

    [Table("t2")]
    public sealed class OwnsUndeclared
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Owned]
        public Undeclared? Value { get; set; }
    }

    [Owned]
    public sealed class Keyed
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Table("t3")]
    public sealed class OwnsAKeyedType
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Owned]
        public Keyed? Value { get; set; }
    }

    [Owned]
    public sealed class Nested
    {
        [Column]
        public string? Name { get; set; }

        [Owned]
        public Point? Inner { get; set; }
    }

    [Table("t4")]
    public sealed class OwnsANestedOwned
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Owned]
        public Nested? Value { get; set; }
    }

    [Table("t5")]
    public sealed class OwnsACollection
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Owned]
        public List<Point>? Points { get; set; }
    }

    [Owned]
    public sealed class Empty
    {
        public string? NotMapped { get; private set; }
    }

    [Table("t6")]
    public sealed class OwnsNothingMapped
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Owned]
        public Empty? Value { get; set; }
    }

    [Table("t7")]
    public sealed class OwnedAndColumn
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Owned]
        [Column]
        public Point? Value { get; set; }
    }

    [Table("t8")]
    public sealed class PrefixCollision
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column("x")]
        public int Direct { get; set; }

        [Owned(Prefix = "")]
        public Point? Value { get; set; }
    }
}
