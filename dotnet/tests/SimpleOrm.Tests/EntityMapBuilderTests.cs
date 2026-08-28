using Xunit;

namespace SimpleOrm.Tests;

/// <summary>The manual loader and the loader precedence (explicit → attribute → convention).</summary>
public sealed class EntityMapBuilderTests
{
    // Deliberately no SimpleOrm attributes: the "can't or won't annotate" case.
    public sealed class Legacy
    {
        public long Id { get; set; }

        public string? DisplayName { get; set; }
    }

    // Plain unannotated type for the convention loader.
    public sealed class Person
    {
        public long Id { get; set; }

        public string? FirstName { get; set; }
    }

    [Fact]
    public void Builder_maps_only_declared_properties_with_explicit_names()
    {
        var builder = new EntityMapBuilder<Legacy>().ToTable("legacy_items");
        builder.Property(x => x.Id).Column("legacy_id").Key().Generated();
        builder.Property(x => x.DisplayName).Column("display_name");

        var options = new MappingOptions().Register(builder);
        var map = new EntityMapLoader(options).Load<Legacy>();

        Assert.Equal("legacy_items", map.RelationName);
        Assert.Equal(KeyStrategy.DatabaseGenerated, map.KeyStrategy);
        Assert.Equal(["legacy_id", "display_name"], map.Properties.Select(p => p.ColumnName));
    }

    [Fact]
    public void Explicit_registration_wins_over_attributes()
    {
        var builder = new EntityMapBuilder<Sample.Models.User>().ToTable("users_manual");
        builder.Property(x => x.Id).Key().Generated();
        builder.Property(x => x.Name);

        var options = new MappingOptions().Register(builder);
        var map = new EntityMapLoader(options).Load<Sample.Models.User>();

        Assert.Equal("users_manual", map.RelationName);
        Assert.Equal(2, map.Properties.Count);
    }

    [Fact]
    public void Convention_loader_maps_unannotated_types()
    {
        var map = new EntityMapLoader().Load<Person>();

        Assert.Equal(RelationKind.Table, map.Kind);
        Assert.Equal("person", map.RelationName);
        Assert.Equal(KeyStrategy.DatabaseGenerated, map.KeyStrategy);
        Assert.Equal(["id", "first_name"], map.Properties.Select(p => p.ColumnName));
        Assert.True(map.Properties.Single(p => p.ColumnName == "first_name").IsNullable);
    }

    [Fact]
    public void Builder_failures_carry_codes_too()
    {
        var builder = new EntityMapBuilder<Legacy>();
        builder.Property(x => x.Id).Column("same");
        builder.Property(x => x.DisplayName).Column("same").Key();

        var loader = new EntityMapLoader(new MappingOptions().Register(builder));
        var exception = Assert.Throws<MappingException>(() => loader.Load<Legacy>());
        Assert.Contains(exception.Errors, e => e.Code == "MAP-018");
    }
}
