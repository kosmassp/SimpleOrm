using System.Reflection;

namespace SimpleOrm;

/// <summary>What backs an entity (ADR-0008): exactly one per class.</summary>
public enum RelationKind
{
    Table,
    View,
    MaterializedView,
    Statement,
    Procedure,
}

/// <summary>How key values come to exist (§7.14).</summary>
public enum KeyStrategy
{
    /// <summary>No key declared (statements, procedures, keyless views).</summary>
    None,

    /// <summary>The database generates the key (<c>INTEGER PRIMARY KEY</c>); inserts read it back via RETURNING.</summary>
    DatabaseGenerated,

    /// <summary>The client supplies a GUID before insert.</summary>
    ClientGuid,

    /// <summary>The caller supplies natural or composite key values.</summary>
    Natural,
}

/// <summary>A declared parameter of a <c>[Statement]</c> entity: SQL-side name and CLR type.</summary>
public sealed class StatementParameter
{
    public StatementParameter(string name, Type clrType)
    {
        Name = name;
        ClrType = clrType;
    }

    public string Name { get; }

    public Type ClrType { get; }
}

/// <summary>One column of a declared index, in index order.</summary>
public sealed class IndexColumn
{
    public IndexColumn(string propertyName, string columnName, bool descending)
    {
        PropertyName = propertyName;
        ColumnName = columnName;
        Descending = descending;
    }

    public string PropertyName { get; }

    public string ColumnName { get; }

    public bool Descending { get; }
}

/// <summary>A declared index (ADR-0007); declaration-only until Level 3 draft migrations.</summary>
public sealed class EntityIndex
{
    public EntityIndex(string name, IReadOnlyList<IndexColumn> columns, bool unique)
    {
        Name = name;
        Columns = columns;
        Unique = unique;
    }

    public string Name { get; }

    public IReadOnlyList<IndexColumn> Columns { get; }

    public bool Unique { get; }
}

/// <summary>
/// A trigger as data (ADR-0042): the name, timing, events, the columns an
/// <c>UPDATE OF</c> narrows to (column names; empty when none), and the one
/// neutral body with its tokens. What a declaration resolves to, what a
/// snapshot records, and what a dialect renders — the migration steps and the
/// derived rollback carry this shape literally, so no step ever holds a
/// dialect's text.
/// </summary>
public sealed class TriggerDefinition
{
    public TriggerDefinition(string name, TriggerTiming timing, TriggerEvent events, string sql, IReadOnlyList<string>? updateOf = null)
    {
        Name = name;
        Timing = timing;
        Events = events;
        Sql = sql;
        UpdateOf = updateOf ?? [];
    }

    public string Name { get; }

    public TriggerTiming Timing { get; }

    public TriggerEvent Events { get; }

    /// <summary>The body verbatim (tokens unexpanded).</summary>
    public string Sql { get; }

    /// <summary>Column names of an <c>UPDATE OF</c> narrowing; empty when the update event is unnarrowed.</summary>
    public IReadOnlyList<string> UpdateOf { get; }

    /// <summary>The events in the fixed order insert, update, delete — the order every rendering and export uses.</summary>
    public IEnumerable<TriggerEvent> EventList()
    {
        foreach (var e in new[] { TriggerEvent.Insert, TriggerEvent.Update, TriggerEvent.Delete })
        {
            if (Events.HasFlag(e))
            {
                yield return e;
            }
        }
    }

    /// <summary>The body with whitespace collapsed and every token in its canonical spelling — how exports and snapshots compare it (layout is not schema).</summary>
    public string NormalizedSql
    {
        get
        {
            var errors = new List<MappingError>();
            var segments = TriggerTokens.Parse(Sql, Name, errors);
            var canonical = errors.Count > 0
                ? Sql   // a malformed body never reaches here through the loader; the data-driven forms compare it as written
                : string.Concat(segments.Select(s => s.Literal ?? TriggerTokens.Canonical(s)));
            return string.Join(" ", canonical.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries));
        }
    }

    /// <summary>Whether two definitions declare the same trigger (name, timing, events, update-of columns, normalized body).</summary>
    public bool SameAs(TriggerDefinition other)
        => string.Equals(Name, other.Name, StringComparison.Ordinal)
            && Timing == other.Timing
            && Events == other.Events
            && UpdateOf.SequenceEqual(other.UpdateOf, StringComparer.Ordinal)
            && NormalizedSql == other.NormalizedSql;
}

/// <summary>A trigger declared on an entity (ADR-0042): the declaration, resolved against the entity, and the class that declares it.</summary>
public sealed class EntityTrigger
{
    public EntityTrigger(Type triggerType, TriggerDefinition definition)
    {
        TriggerType = triggerType;
        Definition = definition;
    }

    /// <summary>The declaration class — the migration object (<c>Migrations/Trigger/&lt;Class&gt;/</c>).</summary>
    public Type TriggerType { get; }

    public TriggerDefinition Definition { get; }

    public string Name => Definition.Name;
}

/// <summary>Navigation cardinality (ADR-0005/0019). Polymorphic and "through" relations are ruled out permanently (ADR-0019 add.1).</summary>
public enum RelationshipKind
{
    ManyToOne,
    OneToMany,
    ManyToMany,
    OneToOne,
}

/// <summary>
/// A declared navigation (ADR-0005, extended by ADR-0019): many-to-one through a
/// foreign key on this class, one-to-many through a foreign key on the target, or
/// many-to-many through an explicit link entity. Declaration-only until Level 2
/// milestone 3 loading.
/// </summary>
public sealed class RelationshipMap
{
    public RelationshipMap(
        string propertyName,
        RelationshipKind kind,
        Type targetType,
        IReadOnlyList<string> foreignKeyProperties,
        Type? linkType = null,
        IReadOnlyList<string>? linkForeignKeysToOwner = null,
        IReadOnlyList<string>? linkForeignKeysToTarget = null,
        CascadeOperations cascade = CascadeOperations.None)
    {
        PropertyName = propertyName;
        Kind = kind;
        TargetType = targetType;
        ForeignKeyProperties = foreignKeyProperties;
        LinkType = linkType;
        LinkForeignKeysToOwner = linkForeignKeysToOwner ?? [];
        LinkForeignKeysToTarget = linkForeignKeysToTarget ?? [];
        Cascade = cascade;
    }

    /// <summary>The unit-of-work operations this navigation cascades (Level 3 M2); declared, never inferred.</summary>
    public CascadeOperations Cascade { get; }

    public string PropertyName { get; }

    public RelationshipKind Kind { get; }

    /// <summary>The related entity type (a collection navigation's element type).</summary>
    public Type TargetType { get; }

    /// <summary>
    /// Many-to-one: the FK properties on this class, in the target's key order.
    /// One-to-many / one-to-one: the FK properties on the target, in this
    /// entity's key order. Empty for many-to-many. One entry per key part —
    /// composite keys carry several (ADR-0019 add.1).
    /// </summary>
    public IReadOnlyList<string> ForeignKeyProperties { get; }

    /// <summary>Many-to-many only: the link entity.</summary>
    public Type? LinkType { get; }

    /// <summary>Many-to-many only: the link properties referencing this class (via [ForeignKey]), in declaration order.</summary>
    public IReadOnlyList<string> LinkForeignKeysToOwner { get; }

    /// <summary>Many-to-many only: the link properties referencing the element type (via [ForeignKey]), in declaration order.</summary>
    public IReadOnlyList<string> LinkForeignKeysToTarget { get; }
}

/// <summary>The declared audit hooks (Level 3 M3, spec/hooks.md): <c>[CreatedAt]</c> fills on insert when unset, <c>[UpdatedAt]</c> stamps on every update.</summary>
public enum AuditRole
{
    None,
    CreatedAt,
    UpdatedAt,
}

/// <summary>One mapped property ↔ column pair.</summary>
public sealed class PropertyMap
{
    public PropertyMap(
        PropertyInfo property,
        string columnName,
        bool isNullable,
        bool isKey,
        bool isGenerated,
        bool isVersion,
        bool enumAsInt,
        Type? foreignKeyReferences,
        OwnedMap? owner = null,
        AuditRole audit = AuditRole.None,
        bool isSoftDelete = false,
        string? renamedFrom = null,
        int? length = null,
        int? precision = null,
        int? scale = null)
    {
        RenamedFrom = renamedFrom;
        Length = length;
        Precision = precision;
        Scale = scale;
        Property = property;
        ColumnName = columnName;
        IsNullable = isNullable;
        IsKey = isKey;
        IsGenerated = isGenerated;
        IsVersion = isVersion;
        EnumAsInt = enumAsInt;
        ForeignKeyReferences = foreignKeyReferences;
        Owner = owner;
        Audit = audit;
        IsSoftDelete = isSoftDelete;
    }

    /// <summary>The audit hook this column carries (Level 3 M3, spec/hooks.md): filled on insert, or stamped on every update.</summary>
    public AuditRole Audit { get; }

    /// <summary>Whether this column is the soft-delete flag (Level 3 M3): a delete sets it instead of removing the row.</summary>
    public bool IsSoftDelete { get; }

    /// <summary>The column this one was previously stored under (Level 3 M5, <c>[RenamedFrom]</c>): a declared rename the diff generator reads; null when none.</summary>
    public string? RenamedFrom { get; }

    /// <summary>The declared length of a string or bytes column (ADR-0043 storage facets, metadata-model.md); null = the dialect's default.</summary>
    public int? Length { get; }

    /// <summary>The declared precision of a decimal column; null = the dialect's default.</summary>
    public int? Precision { get; }

    /// <summary>The declared scale of a decimal column; set only with <see cref="Precision"/> (0 when the declaration omits it).</summary>
    public int? Scale { get; }

    /// <summary>The member itself — on the entity, or on the owned type when <see cref="Owner"/> is set.</summary>
    public PropertyInfo Property { get; }

    /// <summary>The owned navigation this member is flattened through (ADR-0030); null for a direct property.</summary>
    public OwnedMap? Owner { get; }

    /// <summary>The property path: a direct member's name, or <c>Navigation.Member</c> for an owned member — the name criteria and column lists use.</summary>
    public string PropertyName => Owner is null ? Property.Name : Owner.PropertyName + "." + Property.Name;

    /// <summary>Reads the member through its path; an owned member of a null navigation reads as null.</summary>
    public object? GetValue(object entity)
    {
        if (Owner is null)
        {
            return Property.GetValue(entity);
        }

        var owned = Owner.Property.GetValue(entity);
        return owned is null ? null : Property.GetValue(owned);
    }

    /// <summary>Writes the member through its path, creating the owned instance on first write.</summary>
    public void SetValue(object entity, object? value)
    {
        if (Owner is null)
        {
            Property.SetValue(entity, value);
            return;
        }

        var owned = Owner.Property.GetValue(entity);
        if (owned is null)
        {
            owned = Activator.CreateInstance(Owner.OwnedType)!;
            Owner.Property.SetValue(entity, owned);
        }

        Property.SetValue(owned, value);
    }

    public string ColumnName { get; }

    public Type ClrType => Property.PropertyType;

    public bool IsNullable { get; }

    public bool IsKey { get; }

    public bool IsGenerated { get; }

    public bool IsVersion { get; }

    public bool EnumAsInt { get; }

    /// <summary>Entity type this FK column references (<c>[ForeignKey]</c>), if declared.</summary>
    public Type? ForeignKeyReferences { get; }

    // The binding names and labels the session asks for on every write, built
    // once per property (ADR-0048) — exactly the strings the bind sites built.
    private string? _parameterName;
    private string? _declaringLabel;
    private EntityLabel? _entityLabel;

    /// <summary>The generated CRUD parameter name, <c>@column</c>.</summary>
    internal string ParameterName => _parameterName ??= "@" + ColumnName;

    /// <summary>The converter's label naming the declaring class: <c>{DeclaringType}.{PropertyName}</c>.</summary>
    internal string DeclaringLabel => _declaringLabel ??= $"{Property.DeclaringType!.Name}.{PropertyName}";

    /// <summary>The converter's label naming an entity type: <c>{entityType}.{PropertyName}</c>; kept for the type last asked (the map's own, in practice).</summary>
    internal string LabelFor(Type entityType)
    {
        var label = _entityLabel;
        if (label is null || label.Type != entityType)
        {
            _entityLabel = label = new EntityLabel(entityType, $"{entityType.Name}.{PropertyName}");
        }

        return label.Text;
    }

    private sealed class EntityLabel(Type type, string text)
    {
        public Type Type { get; } = type;

        public string Text { get; } = text;
    }
}

/// <summary>
/// An owned value type flattened into its owner's table (ADR-0030): the
/// navigation property, the column prefix its members carry, and the members.
/// An owned type is not an entity — no table, key, version, generated column,
/// relationship, or nested owned type — so every subsystem sees only the
/// flattened <see cref="PropertyMap"/>s; this is how the mapper regroups them.
/// </summary>
public sealed class OwnedMap
{
    private readonly List<PropertyMap> _members = [];

    public OwnedMap(PropertyInfo property, string prefix, bool isNullable)
    {
        Property = property;
        Prefix = prefix;
        IsNullable = isNullable;
    }

    public PropertyInfo Property { get; }

    public string PropertyName => Property.Name;

    public Type OwnedType => Property.PropertyType;

    /// <summary>Prepended to every member's column name; may be empty.</summary>
    public string Prefix { get; }

    /// <summary>
    /// Whether the navigation may be null: then every member column is nullable
    /// and a row whose member columns are all NULL leaves the navigation null.
    /// </summary>
    public bool IsNullable { get; }

    /// <summary>The flattened members, in declaration order; the same instances appear in the owner's property list.</summary>
    public IReadOnlyList<PropertyMap> Members => _members;

    internal void AddMember(PropertyMap member) => _members.Add(member);
}

/// <summary>
/// The single source of truth about a mapped type (§7.1). Produced only by the
/// loaders; every other subsystem reads this and never the attributes.
/// </summary>
public sealed class EntityMap
{
    public EntityMap(
        Type entityType,
        RelationKind kind,
        string? relationName,
        string? schema,
        string? definingSql,
        IReadOnlyList<StatementParameter> statementParameters,
        IReadOnlyList<PropertyMap> properties,
        KeyStrategy keyStrategy,
        IReadOnlyList<EntityIndex> indexes,
        IReadOnlyList<RelationshipMap> relationships,
        IReadOnlyList<EntityTrigger>? triggers = null)
    {
        Triggers = triggers ?? [];
        EntityType = entityType;
        Kind = kind;
        RelationName = relationName;
        Schema = schema;
        DefiningSql = definingSql;
        StatementParameters = statementParameters;
        Properties = properties;
        KeyStrategy = keyStrategy;
        Indexes = indexes;
        Relationships = relationships;
        KeyProperties = properties.Where(p => p.IsKey).ToArray();
        VersionProperty = properties.FirstOrDefault(p => p.IsVersion);
        OwnedTypes = properties.Where(p => p.Owner is not null).Select(p => p.Owner!).Distinct().ToArray();
    }

    /// <summary>The owned value types flattened into this entity (ADR-0030), in declaration order.</summary>
    public IReadOnlyList<OwnedMap> OwnedTypes { get; }

    public Type EntityType { get; }

    public RelationKind Kind { get; }

    /// <summary>Table/view/procedure name; null for statement-backed entities.</summary>
    public string? RelationName { get; }

    public string? Schema { get; }

    /// <summary>The entity's SQL: a statement's query, or a view/materialized view's defining SELECT; null for tables and procedures.</summary>
    public string? DefiningSql { get; }

    public IReadOnlyList<StatementParameter> StatementParameters { get; }

    public IReadOnlyList<PropertyMap> Properties { get; }

    /// <summary>Key properties in declaration order (composite keys are ordered).</summary>
    public IReadOnlyList<PropertyMap> KeyProperties { get; }

    public KeyStrategy KeyStrategy { get; }

    public PropertyMap? VersionProperty { get; }

    /// <summary>The <c>[CreatedAt]</c> column, if declared (Level 3 M3).</summary>
    public PropertyMap? CreatedAtProperty => Properties.FirstOrDefault(p => p.Audit == AuditRole.CreatedAt);

    /// <summary>The <c>[UpdatedAt]</c> column, if declared (Level 3 M3).</summary>
    public PropertyMap? UpdatedAtProperty => Properties.FirstOrDefault(p => p.Audit == AuditRole.UpdatedAt);

    /// <summary>The <c>[SoftDelete]</c> flag, if declared (Level 3 M3): a delete becomes an update setting it.</summary>
    public PropertyMap? SoftDeleteProperty => Properties.FirstOrDefault(p => p.IsSoftDelete);

    public IReadOnlyList<EntityIndex> Indexes { get; }

    public IReadOnlyList<RelationshipMap> Relationships { get; }

    /// <summary>The triggers this relation carries (ADR-0042), in link declaration order; declaration-only — migrations create them.</summary>
    public IReadOnlyList<EntityTrigger> Triggers { get; }

    /// <summary>Entity identity (§7.4): the key values of an instance, in key order.</summary>
    public object?[] GetKeyValues(object entity)
    {
        if (KeyProperties.Count == 0)
        {
            throw new InvalidOperationException($"'{EntityType.Name}' has no key; identity is undefined.");
        }

        var values = new object?[KeyProperties.Count];
        for (var i = 0; i < KeyProperties.Count; i++)
        {
            values[i] = KeyProperties[i].GetValue(entity);
        }

        return values;
    }

    /// <summary>True when two instances have equal key values (§7.4).</summary>
    public bool KeysEqual(object left, object right)
    {
        var leftKeys = GetKeyValues(left);
        var rightKeys = GetKeyValues(right);
        for (var i = 0; i < leftKeys.Length; i++)
        {
            if (!Equals(leftKeys[i], rightKeys[i]))
            {
                return false;
            }
        }

        return true;
    }
}
