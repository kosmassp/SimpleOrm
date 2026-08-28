using System.Reflection;

namespace SimpleOrm;

/// <summary>Loader-internal working shape of one mapped property before assembly.</summary>
internal sealed class MappedPropertySpec
{
    public MappedPropertySpec(PropertyInfo property) => Property = property;

    public PropertyInfo Property { get; }

    public string? ExplicitColumn { get; set; }

    public bool IsKey { get; set; }

    public bool IsGenerated { get; set; }

    public bool IsVersion { get; set; }

    public bool EnumAsInt { get; set; }

    public Type? ForeignKeyReferences { get; set; }
}

/// <summary>Loader-internal working shape of a declared index before column resolution.</summary>
internal sealed class IndexSpec
{
    public IndexSpec(string? name, bool unique, IReadOnlyList<(string PropertyName, bool Descending)> columns)
    {
        Name = name;
        Unique = unique;
        Columns = columns;
    }

    public string? Name { get; }

    public bool Unique { get; }

    public IReadOnlyList<(string PropertyName, bool Descending)> Columns { get; }
}

/// <summary>Loader-internal working shape of a declared many-to-one before validation.</summary>
internal sealed class RelationshipSpec
{
    public RelationshipSpec(string propertyName, Type targetType, string foreignKeyProperty)
    {
        PropertyName = propertyName;
        TargetType = targetType;
        ForeignKeyProperty = foreignKeyProperty;
    }

    public string PropertyName { get; }

    public Type TargetType { get; }

    public string ForeignKeyProperty { get; }
}
