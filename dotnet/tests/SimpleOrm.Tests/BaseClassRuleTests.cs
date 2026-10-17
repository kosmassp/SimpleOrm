using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// ADR-0040: a base class shares members only; identity comes from the concrete class
/// alone. An abstract class loaded as an entity by any path, a mapped type whose base
/// carries a relation source, and — in a scan — a concrete sourceless class another
/// scanned type extends are all <c>MAP-028</c>.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class BaseClassRuleTests(SqliteFixture fixture)
{
    private static IEnumerable<string> CodesOf<T>()
        where T : class
        => Assert.Throws<MappingException>(() => new EntityMapLoader().Load<T>()).Errors.Select(e => e.Code);

    [Fact]
    public void An_abstract_class_never_loads_as_an_entity()
    {
        var error = Assert.Single(Assert.Throws<MappingException>(() => new EntityMapLoader().Load<BaseModel>()).Errors);
        Assert.Equal("MAP-028", error.Code);
        Assert.Equal(nameof(BaseModel), error.Target);

        // An abstract class without any mapping attribute would otherwise go to the
        // convention loader; it is refused the same way.
        Assert.Equal(["MAP-028"], CodesOf<PlainAbstract>());

        // The concrete class still maps, its inherited columns included (ADR-0040 keeps what exists).
        var user = new EntityMapLoader().Load<User>();
        Assert.Contains(user.Properties, p => p.PropertyName == nameof(BaseModel.CreatedAtUtc) && p.ColumnName == "created_at");
    }

    [Fact]
    public async Task Every_read_write_and_load_path_through_an_abstract_class_is_MAP028()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        var registry = Query.Inline("select created_at, updated_at from users");
        Assert.Equal("MAP-028", await CodeOf(() => db.QueryAsync<EmptyArgs, BaseModel>(registry, EmptyArgs.Value, CancellationToken.None)));
        Assert.Equal("MAP-028", await CodeOf(() => db.GetAsync<BaseModel>(ada.Id, CancellationToken.None)));
        Assert.Equal("MAP-028", await CodeOf(() => db.GetOrDefaultAsync<BaseModel>(ada.Id, CancellationToken.None)));
        Assert.Equal("MAP-028", await CodeOf(() => db.Query<BaseModel>().ToListAsync(CancellationToken.None)));
        Assert.Equal("MAP-028", await CodeOf(() => db.InsertAsync<BaseModel>(ada, CancellationToken.None)));
        Assert.Equal("MAP-028", await CodeOf(() => db.UpdateAsync<BaseModel>(ada, CancellationToken.None)));
        Assert.Equal("MAP-028", await CodeOf(() => db.DeleteAsync<BaseModel>(ada.Id, CancellationToken.None)));
        Assert.Equal("MAP-028", await CodeOf(() => db.Query<BaseModel>().Include(nameof(User.Roles)).ToListAsync(CancellationToken.None)));

        // A navigation target typed to an abstract class: the owner maps (the declaration
        // is shape-valid), the load that would materialize the target refuses.
        var note = new NoteOnAnAbstractTarget { UserId = ada.Id };
        Assert.Equal("MAP-028", await CodeOf(() => db.LoadAsync(note, nameof(NoteOnAnAbstractTarget.Target), CancellationToken.None)));

        // The tracker and the export go through the same entry point.
        await using var tracked = await Db.OpenAsync(
            fixture.ConnectionString, new DbOptions { Dialect = TestDb.Options.Dialect, Tracking = TrackingMode.Tracked }, CancellationToken.None);
        Assert.Equal("MAP-028", await CodeOf(() => tracked.GetAsync<BaseModel>(ada.Id, CancellationToken.None)));
        Assert.Equal("MAP-028", Assert.Throws<MappingException>(() => EntityMapJson.Export(db.Maps.Load<BaseModel>(), db.Maps)).Errors[0].Code);
    }

    [Fact]
    public void The_builder_refuses_an_abstract_registration()
    {
        var exception = Assert.Throws<MappingException>(
            () => new MappingOptions().Register(new EntityMapBuilder<BaseModel>().ToTable("base_model")));
        Assert.Equal("MAP-028", Assert.Single(exception.Errors).Code);
        Assert.Equal(typeof(BaseModel), exception.EntityType);
    }

    [Fact]
    public void A_base_class_carrying_a_relation_source_is_MAP028_naming_both_classes()
    {
        var exception = Assert.Throws<MappingException>(() => new EntityMapLoader().Load<ExtendsATable>());
        var error = Assert.Single(exception.Errors);
        Assert.Equal("MAP-028", error.Code);
        Assert.Equal(nameof(ExtendsATable), error.Target);
        Assert.Contains(nameof(ConcreteTableBase), error.Message);
        Assert.Contains("[Table]", error.Message);

        Assert.Contains("MAP-028", CodesOf<ExtendsAView>());             // [View] on the base
        Assert.Contains("MAP-028", CodesOf<ExtendsAStatement>());        // [Statement] on the base
        Assert.Contains("MAP-028", CodesOf<ExtendsAnIndexedBase>());     // [Index] on the base: it belongs to a relation
        Assert.Contains("MAP-028", CodesOf<ExtendsATableTwoLevelsDown>()); // anywhere up the chain
        Assert.Contains("MAP-028", CodesOf<ExtendsAnAbstractTable>());   // abstract + [Table]: the base's source still names it an entity

        // The subclass never adopts the base's table — refused, not silently remapped —
        // and the check is one error among the type's others, never first-error-only.
        Assert.Equal(["MAP-028", "MAP-010"], CodesOf<ExtendsATableWithAnUnmappedProperty>().OrderDescending().ToArray());

        // The rule holds under every loader: a manual map of such a subclass is refused the same way.
        var options = new MappingOptions().Register(new EntityMapBuilder<ExtendsATable>().ToTable("labels"));
        Assert.Contains("MAP-028", Assert.Throws<MappingException>(() => new EntityMapLoader(options).Load<ExtendsATable>()).Errors.Select(e => e.Code));
    }

    [Fact]
    public void A_concrete_sourceless_base_maps_alone_but_fails_the_scan()
    {
        // Alone, a concrete class with [Column] properties and no source is a table
        // named by convention — nothing a lone load can object to.
        var alone = new EntityMapLoader().Load<ConcreteSourcelessBase>();
        Assert.Equal("concrete_sourceless_base", alone.RelationName);

        // Seen together with a type that extends it, it is a base class: one error,
        // naming the extending types, and nothing else reported about it.
        var errors = EntityMapLoader.BaseClassesUsedAsEntities(
            [typeof(ConcreteSourcelessBase), typeof(ExtendsTheSourcelessBase), typeof(AlsoExtendsTheSourcelessBase), typeof(User)]);
        var error = Assert.Single(errors);
        Assert.Equal("MAP-028", error.Code);
        Assert.Equal(nameof(ConcreteSourcelessBase), error.Target);
        Assert.Contains(nameof(ExtendsTheSourcelessBase), error.Message);
        Assert.Contains(nameof(AlsoExtendsTheSourcelessBase), error.Message);

        // The fixture itself is clean: BaseModel is abstract, so nothing in the sample is flagged.
        Assert.Empty(EntityMapLoader.BaseClassesUsedAsEntities(typeof(User).Assembly.GetExportedTypes()));

        // Tooling enumerates entities through the scan: the whole set refuses when a base class is among it.
        var refused = Assert.Throws<MappingException>(() => EntityMapLoader.ScannedEntityTypes(
            [typeof(ConcreteSourcelessBase), typeof(ExtendsTheSourcelessBase), typeof(User)]));
        Assert.Equal(typeof(ConcreteSourcelessBase), refused.EntityType);
        Assert.DoesNotContain(typeof(BaseModel), EntityMapLoader.ScannedEntityTypes(typeof(User).Assembly.GetExportedTypes()));
    }

    [Fact]
    public async Task SchemaGuard_reports_the_scan_rule_in_the_complete_report()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var exception = await Assert.ThrowsAsync<SchemaValidationException>(
            () => SchemaGuard.ValidateTypesAsync(
                db, [typeof(ConcreteSourcelessBase), typeof(ExtendsTheSourcelessBase), typeof(RegistryReadingABaseClass)], CancellationToken.None));

        Assert.Contains(exception.Errors, e => e.Code == "MAP-028" && e.Source == nameof(ConcreteSourcelessBase));
        Assert.DoesNotContain(exception.Errors, e => e.Code == "VAL-012" && e.Source == nameof(ConcreteSourcelessBase));   // not validated as a table
        Assert.Contains(exception.Errors, e => e.Code == "VAL-012" && e.Source == nameof(ExtendsTheSourcelessBase));       // the subclass still is
        Assert.Contains(exception.Errors, e => e.Code == "MAP-028" && e.Source == "RegistryReadingABaseClass.Audit");       // a registry result typed to BaseModel

        // The assembly scan passes on the fixture, whose base class is abstract.
        await SchemaGuard.ValidateAsync(db, typeof(User).Assembly, CancellationToken.None);
    }

    private static async Task<string> CodeOf(Func<Task> action)
    {
        try
        {
            await action();
            return "(no error)";
        }
        catch (MappingException exception)
        {
            return exception.Errors[0].Code;
        }
        catch (SimpleOrmException exception)
        {
            return exception.Code;
        }
    }

    // --- fixtures --------------------------------------------------------------------

    public abstract class PlainAbstract
    {
        public long Id { get; set; }
    }

    [Table("concrete_table_bases")]
    public class ConcreteTableBase
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }
    }

    public sealed class ExtendsATable : ConcreteTableBase
    {
        [Column]
        public string? Label { get; set; }
    }

    public class MidLevel : ConcreteTableBase
    {
    }

    public sealed class ExtendsATableTwoLevelsDown : MidLevel
    {
        [Column]
        public string? Label { get; set; }
    }

    [Table("abstract_table_bases")]
    public abstract class AbstractTableBase
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    public sealed class ExtendsAnAbstractTable : AbstractTableBase
    {
    }

    public sealed class ExtendsATableWithAnUnmappedProperty : ConcreteTableBase
    {
        public string? Unmapped { get; set; }   // MAP-010 beside the MAP-028
    }

    [View("a_view", "select 1 as id")]
    public class ViewBase
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    public sealed class ExtendsAView : ViewBase
    {
    }

    [Statement("select 1 as id")]
    public class StatementBase
    {
        [Column]
        public long Id { get; set; }
    }

    public sealed class ExtendsAStatement : StatementBase
    {
    }

    [Index(nameof(Id))]
    public class IndexedBase
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    public sealed class ExtendsAnIndexedBase : IndexedBase
    {
    }

    /// <summary>Legitimate alone (a convention-named table); a base class once something extends it.</summary>
    public class ConcreteSourcelessBase
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }
    }

    public sealed class ExtendsTheSourcelessBase : ConcreteSourcelessBase
    {
        [Column]
        public string? Label { get; set; }
    }

    public sealed class AlsoExtendsTheSourcelessBase : ConcreteSourcelessBase
    {
    }

    [Table("notes_on_abstract_targets")]
    public sealed class NoteOnAnAbstractTarget
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public long UserId { get; set; }

        [ManyToOne(nameof(UserId))]
        public BaseModel? Target { get; private set; }
    }

    public static class RegistryReadingABaseClass
    {
        public static readonly Query<EmptyArgs, BaseModel> Audit = Query.Inline("select created_at, updated_at from users");
    }
}
