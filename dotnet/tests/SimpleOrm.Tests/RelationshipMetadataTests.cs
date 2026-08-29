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
    public void User_declares_all_navigation_kinds()
    {
        var map = Loader.Load<User>();
        Assert.Equal(3, map.Relationships.Count);

        var transactions = Assert.Single(map.Relationships, r => r.PropertyName == nameof(User.Transactions));
        Assert.Equal(RelationshipKind.OneToMany, transactions.Kind);
        Assert.Equal(typeof(Transaction), transactions.TargetType);
        Assert.Equal([nameof(Transaction.UserId)], transactions.ForeignKeyProperties);

        var roles = Assert.Single(map.Relationships, r => r.PropertyName == nameof(User.Roles));
        Assert.Equal(RelationshipKind.ManyToMany, roles.Kind);
        Assert.Equal(typeof(Role), roles.TargetType);
        Assert.Equal(typeof(UserRole), roles.LinkType);
        Assert.Equal([nameof(UserRole.UserId)], roles.LinkForeignKeysToOwner);
        Assert.Equal([nameof(UserRole.RoleId)], roles.LinkForeignKeysToTarget);

        // The fourth classic cardinality (ADR-0019 add.1): singular inverse, FK on the target.
        var profile = Assert.Single(map.Relationships, r => r.PropertyName == nameof(User.Profile));
        Assert.Equal(RelationshipKind.OneToOne, profile.Kind);
        Assert.Equal(typeof(UserProfile), profile.TargetType);
        Assert.Equal([nameof(UserProfile.UserId)], profile.ForeignKeyProperties);
    }

    [Fact]
    public void Transaction_keeps_many_to_one_beside_the_collection()
    {
        var map = Loader.Load<Transaction>();
        var user = Assert.Single(map.Relationships, r => r.PropertyName == nameof(Transaction.User));
        Assert.Equal(RelationshipKind.ManyToOne, user.Kind);
        Assert.Equal([nameof(Transaction.UserId)], user.ForeignKeyProperties);

        var details = Assert.Single(map.Relationships, r => r.PropertyName == nameof(Transaction.Details));
        Assert.Equal(RelationshipKind.OneToMany, details.Kind);
        Assert.Equal(typeof(TransactionDetail), details.TargetType);
    }

    [Fact]
    public void Composite_key_target_takes_a_foreign_key_list_in_key_order()
    {
        // UserRole's key is (UserId, RoleId): the referencing side declares both,
        // in that order (ADR-0019 add.1).
        var map = Loader.Load<CompositeReferenceFixture>();
        var grant = Assert.Single(map.Relationships);
        Assert.Equal(RelationshipKind.ManyToOne, grant.Kind);
        Assert.Equal(typeof(UserRole), grant.TargetType);
        Assert.Equal(["UserId", "RoleId"], grant.ForeignKeyProperties);
    }

    [Fact]
    public void Foreign_key_arity_must_match_the_target_key() => AssertCode<CompositeArityFixture>("MAP-016");

    [Fact]
    public void One_to_one_on_a_collection_is_MAP020() => AssertCode<Map020OneToOneFixture>("MAP-020");

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
        [ForeignKey(typeof(Map022AmbiguousFixture))]
        public long WidgetId { get; set; }

        [Key]
        [Column]
        [ForeignKey(typeof(Map022AmbiguousFixture))]
        public long OtherWidgetId { get; set; }

        [Column]
        [ForeignKey(typeof(Role))]
        public long RoleId { get; set; }
    }

    [Table("m22_ambiguous_widgets")]
    private sealed class Map022AmbiguousFixture
    {
        // Single-part key, but the link declares two [ForeignKey]s to this type:
        // the FK count must equal the key arity (ADR-0019 add.1).
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [ManyToMany(typeof(AmbiguousLink))]
        public IReadOnlyList<Role> Roles { get; private set; } = [];
    }

    [Table("composite_grants")]
    private sealed class CompositeReferenceFixture
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public long UserId { get; set; }

        [Column]
        public long RoleId { get; set; }

        [ManyToOne(nameof(UserId), nameof(RoleId))]
        public UserRole? Grant { get; private set; }
    }

    [Table("composite_arity_widgets")]
    private sealed class CompositeArityFixture
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public long UserId { get; set; }

        [ManyToOne(nameof(UserId))]   // UserRole's key has two parts
        public UserRole? Grant { get; private set; }
    }

    [Table("m20_one_to_one_widgets")]
    private sealed class Map020OneToOneFixture
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [OneToOne(nameof(Transaction.UserId))]
        public IReadOnlyList<Transaction> Child { get; private set; } = [];   // a collection is not one-to-one
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
