using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Level 2 milestone 1 (ADR-0019): declaration-only relationship metadata.
/// [OneToMany] resolves its element type and target FK; [ManyToMany] resolves both
/// sides through the link's [ForeignKey] declarations, exactly once each; every
/// invalid declaration has a named code. Nothing loads yet — no hidden queries.
/// </summary>
public sealed class RelationshipMetadataTests
{
    private static readonly EntityMapLoader Loader = new();

    [Fact]
    public void User_declares_one_to_many_and_many_to_many()
    {
        var map = Loader.Load<User>();
        Assert.Equal(2, map.Relationships.Count);

        var transactions = Assert.Single(map.Relationships, r => r.PropertyName == nameof(User.Transactions));
        Assert.Equal(RelationshipKind.OneToMany, transactions.Kind);
        Assert.Equal(typeof(Transaction), transactions.TargetType);
        Assert.Equal(nameof(Transaction.UserId), transactions.ForeignKeyProperty);

        var roles = Assert.Single(map.Relationships, r => r.PropertyName == nameof(User.Roles));
        Assert.Equal(RelationshipKind.ManyToMany, roles.Kind);
        Assert.Equal(typeof(Role), roles.TargetType);
        Assert.Equal(typeof(UserRole), roles.LinkType);
        Assert.Equal(nameof(UserRole.UserId), roles.LinkForeignKeyToOwner);
        Assert.Equal(nameof(UserRole.RoleId), roles.LinkForeignKeyToTarget);
    }

    [Fact]
    public void Transaction_keeps_many_to_one_beside_the_collection()
    {
        var map = Loader.Load<Transaction>();
        var user = Assert.Single(map.Relationships, r => r.PropertyName == nameof(Transaction.User));
        Assert.Equal(RelationshipKind.ManyToOne, user.Kind);
        Assert.Equal(nameof(Transaction.UserId), user.ForeignKeyProperty);

        var details = Assert.Single(map.Relationships, r => r.PropertyName == nameof(Transaction.Details));
        Assert.Equal(RelationshipKind.OneToMany, details.Kind);
        Assert.Equal(typeof(TransactionDetail), details.TargetType);
    }

    [Fact]
    public void Navigations_are_transient_and_never_columns()
    {
        var map = Loader.Load<User>();
        Assert.DoesNotContain(map.Properties, p => p.PropertyName is nameof(User.Transactions) or nameof(User.Roles));
    }

    [Fact]
    public void Non_collection_navigation_is_MAP020() => AssertCode<Map020Fixture>("MAP-020");

    [Fact]
    public void Unknown_target_foreign_key_is_MAP021() => AssertCode<Map021Fixture>("MAP-021");

    [Fact]
    public void Link_missing_a_side_is_MAP022() => AssertCode<Map022MissingFixture>("MAP-022");

    [Fact]
    public void Link_with_an_ambiguous_side_is_MAP022() => AssertCode<Map022AmbiguousFixture>("MAP-022");

    [Fact]
    public void Public_setter_on_a_collection_navigation_is_MAP011() => AssertCode<Map011CollectionFixture>("MAP-011");

    [Fact]
    public void Column_on_a_navigation_is_MAP019() => AssertCode<Map019NavigationFixture>("MAP-019");

    private static void AssertCode<T>(string code)
        where T : class
    {
        var exception = Assert.Throws<MappingException>(() => new EntityMapLoader().Load<T>());
        Assert.Contains(exception.Errors, e => e.Code == code);
    }

    // --- failing fixtures -----------------------------------------------------------

    [Table("m20_widgets")]
    private sealed class Map020Fixture
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [OneToMany(nameof(User.Id))]
        public string Children { get; private set; } = string.Empty;   // not a collection of an entity
    }

    [Table("m21_widgets")]
    private sealed class Map021Fixture
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [OneToMany("NoSuchProperty")]
        public IReadOnlyList<Transaction> Children { get; private set; } = [];
    }

    [Table("m22_missing_widgets")]
    private sealed class Map022MissingFixture
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        // UserRole's [ForeignKey] declarations reference User and Role — neither side is this type.
        [ManyToMany(typeof(UserRole))]
        public IReadOnlyList<Role> Roles { get; private set; } = [];
    }

    [Table("m22_links")]
    private sealed class AmbiguousLink
    {
        [Key]
        [Column]
        [ForeignKey(typeof(User))]
        public long UserId { get; set; }

        [Key]
        [Column]
        [ForeignKey(typeof(User))]
        public long OtherUserId { get; set; }

        [Column]
        [ForeignKey(typeof(Role))]
        public long RoleId { get; set; }
    }

    [Table("m22_ambiguous_widgets")]
    private sealed class Map022AmbiguousFixture
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [ManyToMany(typeof(AmbiguousLink))]
        public IReadOnlyList<Role> Roles { get; private set; } = [];
    }

    [Table("m11_widgets")]
    private sealed class Map011CollectionFixture
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [OneToMany(nameof(Transaction.UserId))]
        public IReadOnlyList<Transaction> Children { get; set; } = [];   // public setter
    }

    [Table("m19_widgets")]
    private sealed class Map019NavigationFixture
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        [OneToMany(nameof(Transaction.UserId))]
        public IReadOnlyList<Transaction> Children { get; private set; } = [];
    }
}
