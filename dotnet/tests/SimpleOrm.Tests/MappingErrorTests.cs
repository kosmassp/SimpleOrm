using Xunit;

namespace SimpleOrm.Tests;

/// <summary>Every loader error code (spec/errors.md) has a fixture that must fail with it.</summary>
public sealed class MappingErrorTests
{
    private static IReadOnlyList<MappingError> ErrorsOf<T>()
        where T : class
        => Assert.Throws<MappingException>(() => new EntityMapLoader().Load<T>()).Errors;

    private static void AssertCode<T>(string code)
        where T : class
        => Assert.Contains(ErrorsOf<T>(), e => e.Code == code);

    [Fact]
    public void Unannotated_public_settable_property_is_MAP010() => AssertCode<Map010Fixture>("MAP-010");

    [Fact]
    public void Navigation_with_public_setter_is_MAP011() => AssertCode<Map011Fixture>("MAP-011");

    [Fact]
    public void Two_relation_sources_is_MAP012() => AssertCode<Map012Fixture>("MAP-012");

    [Fact]
    public void Version_on_view_is_MAP013() => AssertCode<Map013ViewFixture>("MAP-013");

    [Fact]
    public void Key_on_statement_is_MAP013() => AssertCode<Map013StatementFixture>("MAP-013");

    [Fact]
    public void Index_on_view_is_MAP014() => AssertCode<Map014Fixture>("MAP-014");

    [Fact]
    public void Index_with_unknown_property_is_MAP015() => AssertCode<Map015UnknownFixture>("MAP-015");

    [Fact]
    public void Index_with_leading_sort_order_is_MAP015() => AssertCode<Map015LeadingFixture>("MAP-015");

    [Fact]
    public void Index_with_doubled_sort_order_is_MAP015() => AssertCode<Map015DoubledFixture>("MAP-015");

    [Fact]
    public void Index_with_alien_token_is_MAP015() => AssertCode<Map015TokenFixture>("MAP-015");

    [Fact]
    public void ManyToOne_with_unknown_fk_is_MAP016() => AssertCode<Map016Fixture>("MAP-016");

    [Fact]
    public void Statement_with_odd_parameter_tokens_is_MAP017() => AssertCode<Map017Fixture>("MAP-017");

    [Fact]
    public void Duplicate_column_is_MAP018() => AssertCode<Map018Fixture>("MAP-018");

    [Fact]
    public void Table_without_key_is_MAP019() => AssertCode<Map019NoKeyFixture>("MAP-019");

    [Fact]
    public void Version_of_wrong_type_is_MAP019() => AssertCode<Map019VersionFixture>("MAP-019");

    [Fact]
    public void Generated_on_composite_key_is_MAP019() => AssertCode<Map019CompositeFixture>("MAP-019");

    [Fact]
    public void Key_without_column_is_MAP019() => AssertCode<Map019BareKeyFixture>("MAP-019");

    [Fact]
    public void EnumAsInt_on_non_enum_is_MAP019() => AssertCode<Map019EnumFixture>("MAP-019");

    [Fact]
    public void Undeclared_placeholder_is_PRM010() => AssertCode<Prm010Fixture>("PRM-010");

    [Fact]
    public void Unused_declared_parameter_is_PRM011() => AssertCode<Prm011Fixture>("PRM-011");

    [Fact]
    public void All_violations_are_collected_before_throwing()
    {
        var errors = ErrorsOf<MultiErrorFixture>();
        Assert.Contains(errors, e => e.Code == "MAP-010");
        Assert.Contains(errors, e => e.Code == "MAP-019");
    }

    // --- fixtures -----------------------------------------------------------

    [Table("f")]
    private sealed class Map010Fixture
    {
        [Key]
        [Column]
        public long Id { get; set; }

        public string? Forgotten { get; set; }
    }

    [Table("f")]
    private sealed class Map011Fixture
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        public long OtherId { get; set; }

        [ManyToOne(nameof(OtherId))]
        public Map010Fixture? Other { get; set; }
    }

    [Table("f")]
    [View("f")]
    private sealed class Map012Fixture
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [View("f")]
    private sealed class Map013ViewFixture
    {
        [Column]
        [Version]
        public long Version { get; set; }
    }

    [Statement("select 1 as id")]
    private sealed class Map013StatementFixture
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [View("f")]
    [Index(nameof(Id))]
    private sealed class Map014Fixture
    {
        [Column]
        public long Id { get; set; }
    }

    [Table("f")]
    [Index("Missing")]
    private sealed class Map015UnknownFixture
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Table("f")]
    [Index(SortOrder.Desc, nameof(Id))]
    private sealed class Map015LeadingFixture
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Table("f")]
    [Index(nameof(Id), SortOrder.Desc, SortOrder.Asc)]
    private sealed class Map015DoubledFixture
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Table("f")]
    [Index(nameof(Id), 42)]
    private sealed class Map015TokenFixture
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Table("f")]
    private sealed class Map016Fixture
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [ManyToOne("Nope")]
        public Map010Fixture? Other { get; private set; }
    }

    [Statement("select 1 as id", "lonely")]
    private sealed class Map017Fixture
    {
        [Column]
        public long Id { get; set; }
    }

    [Table("f")]
    private sealed class Map018Fixture
    {
        [Key]
        [Column("same")]
        public long Id { get; set; }

        [Column("same")]
        public string? Twin { get; set; }
    }

    [Table("f")]
    private sealed class Map019NoKeyFixture
    {
        [Column]
        public string? Value { get; set; }
    }

    [Table("f")]
    private sealed class Map019VersionFixture
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        [Version]
        public string? Version { get; set; }
    }

    [Table("f")]
    private sealed class Map019CompositeFixture
    {
        [Key]
        [Generated]
        [Column]
        public long Left { get; set; }

        [Key]
        [Column]
        public long Right { get; set; }
    }

    [Table("f")]
    private sealed class Map019BareKeyFixture
    {
        [Key]
        public long Id { get; set; }

        [Column]
        public string? Value { get; set; }
    }

    [Table("f")]
    private sealed class Map019EnumFixture
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        [EnumAsInt]
        public string? NotAnEnum { get; set; }
    }

    [Statement("select 1 as id where x = @mystery")]
    private sealed class Prm010Fixture
    {
        [Column]
        public long Id { get; set; }
    }

    [Statement("select 1 as id", "unused", typeof(int))]
    private sealed class Prm011Fixture
    {
        [Column]
        public long Id { get; set; }
    }

    [Table("f")]
    private sealed class MultiErrorFixture
    {
        [Column]
        public string? Value { get; set; }

        public string? Forgotten { get; set; }
    }
}
