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

/// <summary>A declared many-to-one navigation (ADR-0005); declaration-only until Level 2 loading.</summary>
public sealed class RelationshipMap
{
    public RelationshipMap(string propertyName, Type targetType, string foreignKeyProperty)
    {
        PropertyName = propertyName;
        TargetType = targetType;
        ForeignKeyProperty = foreignKeyProperty;
    }

    public string PropertyName { get; }

    public Type TargetType { get; }

    public string ForeignKeyProperty { get; }
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
        Type? foreignKeyReferences)
    {
        Property = property;
        ColumnName = columnName;
        IsNullable = isNullable;
        IsKey = isKey;
        IsGenerated = isGenerated;
        IsVersion = isVersion;
        EnumAsInt = enumAsInt;
        ForeignKeyReferences = foreignKeyReferences;
    }

    public PropertyInfo Property { get; }

    public string PropertyName => Property.Name;

    public string ColumnName { get; }

    public Type ClrType => Property.PropertyType;

    public bool IsNullable { get; }

    public bool IsKey { get; }

    public bool IsGenerated { get; }

    public bool IsVersion { get; }

    public bool EnumAsInt { get; }

    /// <summary>Entity type this FK column references (<c>[ForeignKey]</c>), if declared.</summary>
    public Type? ForeignKeyReferences { get; }
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
        string? statementSql,
        IReadOnlyList<StatementParameter> statementParameters,
        IReadOnlyList<PropertyMap> properties,
        KeyStrategy keyStrategy,
        IReadOnlyList<EntityIndex> indexes,
        IReadOnlyList<RelationshipMap> relationships)
    {
        EntityType = entityType;
        Kind = kind;
        RelationName = relationName;
        Schema = schema;
        StatementSql = statementSql;
        StatementParameters = statementParameters;
        Properties = properties;
        KeyStrategy = keyStrategy;
        Indexes = indexes;
        Relationships = relationships;
        KeyProperties = properties.Where(p => p.IsKey).ToArray();
        VersionProperty = properties.FirstOrDefault(p => p.IsVersion);
    }

    public Type EntityType { get; }

    public RelationKind Kind { get; }

    /// <summary>Table/view/procedure name; null for statement-backed entities.</summary>
    public string? RelationName { get; }

    public string? Schema { get; }

    /// <summary>The SQL of a statement-backed entity; null otherwise.</summary>
    public string? StatementSql { get; }

    public IReadOnlyList<StatementParameter> StatementParameters { get; }

    public IReadOnlyList<PropertyMap> Properties { get; }

    /// <summary>Key properties in declaration order (composite keys are ordered).</summary>
    public IReadOnlyList<PropertyMap> KeyProperties { get; }

    public KeyStrategy KeyStrategy { get; }

    public PropertyMap? VersionProperty { get; }

    public IReadOnlyList<EntityIndex> Indexes { get; }

    public IReadOnlyList<RelationshipMap> Relationships { get; }

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
            values[i] = KeyProperties[i].Property.GetValue(entity);
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
