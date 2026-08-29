using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// Shared final stage of every loader path: resolves column names through the
/// convention, runs the source-independent validations (duplicate columns, key
/// shape, version shape, relationship FK resolution, index column resolution), and
/// produces the <see cref="EntityMap"/>. Violations accumulate in <c>errors</c>;
/// the caller throws one <see cref="MappingException"/> with all of them.
/// </summary>
internal static class MapAssembler
{
    public static EntityMap? Assemble(
        Type entityType,
        RelationKind kind,
        string? relationName,
        string? schema,
        string? statementSql,
        IReadOnlyList<StatementParameter> statementParameters,
        IReadOnlyList<MappedPropertySpec> specs,
        IReadOnlyList<IndexSpec> indexSpecs,
        IReadOnlyList<RelationshipSpec> relationshipSpecs,
        INamingConvention convention,
        List<MappingError> errors)
    {
        var properties = new List<PropertyMap>(specs.Count);
        var byProperty = new Dictionary<string, PropertyMap>();
        var seenColumns = new Dictionary<string, string>();

        foreach (var spec in specs)
        {
            var column = spec.ExplicitColumn ?? convention.ColumnName(spec.Property.Name);
            if (seenColumns.TryGetValue(column, out var other))
            {
                errors.Add(new MappingError(
                    "MAP-018",
                    $"{entityType.Name}.{spec.Property.Name}",
                    $"maps to column '{column}' already used by '{other}'"));
            }
            else
            {
                seenColumns.Add(column, spec.Property.Name);
            }

            var map = new PropertyMap(
                spec.Property,
                column,
                NullabilityReader.IsNullable(spec.Property),
                spec.IsKey,
                spec.IsGenerated,
                spec.IsVersion,
                spec.EnumAsInt,
                spec.ForeignKeyReferences);
            properties.Add(map);
            byProperty[spec.Property.Name] = map;
        }

        ValidateVersion(entityType, properties, errors);
        var keyStrategy = ResolveKeyStrategy(entityType, kind, properties, errors);
        var indexes = ResolveIndexes(entityType, relationName, indexSpecs, byProperty, convention, errors);
        var relationships = ResolveRelationships(entityType, relationshipSpecs, byProperty, errors);

        if (errors.Count > 0)
        {
            return null;
        }

        return new EntityMap(
            entityType,
            kind,
            relationName,
            schema,
            statementSql,
            statementParameters,
            properties,
            keyStrategy,
            indexes,
            relationships);
    }

    private static void ValidateVersion(Type entityType, List<PropertyMap> properties, List<MappingError> errors)
    {
        PropertyMap? version = null;
        foreach (var property in properties.Where(p => p.IsVersion))
        {
            if (version is not null)
            {
                errors.Add(new MappingError(
                    "MAP-019",
                    $"{entityType.Name}.{property.PropertyName}",
                    $"a second [Version] property; '{version.PropertyName}' already is the version column"));
                continue;
            }

            version = property;
            if (property.ClrType != typeof(int) && property.ClrType != typeof(long))
            {
                errors.Add(new MappingError(
                    "MAP-019",
                    $"{entityType.Name}.{property.PropertyName}",
                    $"[Version] requires int or long, found {property.ClrType.Name}"));
            }

            if (property.IsKey)
            {
                errors.Add(new MappingError(
                    "MAP-019",
                    $"{entityType.Name}.{property.PropertyName}",
                    "[Version] cannot be part of the key"));
            }
        }
    }

    private static KeyStrategy ResolveKeyStrategy(
        Type entityType, RelationKind kind, List<PropertyMap> properties, List<MappingError> errors)
    {
        var keys = properties.Where(p => p.IsKey).ToArray();
        if (keys.Length == 0)
        {
            if (kind == RelationKind.Table)
            {
                errors.Add(new MappingError(
                    "MAP-019", entityType.Name, "a table-backed entity must declare a key"));
            }

            return KeyStrategy.None;
        }

        var generatedKeys = keys.Where(k => k.IsGenerated).ToArray();
        if (generatedKeys.Length > 0)
        {
            if (keys.Length > 1)
            {
                errors.Add(new MappingError(
                    "MAP-019",
                    $"{entityType.Name}.{generatedKeys[0].PropertyName}",
                    "[Generated] is not valid on a composite key"));
            }

            return KeyStrategy.DatabaseGenerated;
        }

        if (keys.Length == 1 && keys[0].ClrType == typeof(Guid))
        {
            return KeyStrategy.ClientGuid;
        }

        return KeyStrategy.Natural;
    }

    private static IReadOnlyList<EntityIndex> ResolveIndexes(
        Type entityType,
        string? relationName,
        IReadOnlyList<IndexSpec> indexSpecs,
        Dictionary<string, PropertyMap> byProperty,
        INamingConvention convention,
        List<MappingError> errors)
    {
        var indexes = new List<EntityIndex>(indexSpecs.Count);
        foreach (var spec in indexSpecs)
        {
            var columns = new List<IndexColumn>(spec.Columns.Count);
            var valid = true;
            foreach (var (propertyName, descending) in spec.Columns)
            {
                if (!byProperty.TryGetValue(propertyName, out var property))
                {
                    errors.Add(new MappingError(
                        "MAP-015",
                        $"{entityType.Name} [Index]",
                        $"'{propertyName}' is not a mapped property"));
                    valid = false;
                    continue;
                }

                columns.Add(new IndexColumn(propertyName, property.ColumnName, descending));
            }

            if (!valid)
            {
                continue;
            }

            var name = spec.Name
                ?? convention.IndexName(relationName ?? string.Empty, columns.Select(c => c.ColumnName).ToArray());
            indexes.Add(new EntityIndex(name, columns, spec.Unique));
        }

        return indexes;
    }

    private static IReadOnlyList<RelationshipMap> ResolveRelationships(
        Type entityType,
        IReadOnlyList<RelationshipSpec> relationshipSpecs,
        Dictionary<string, PropertyMap> byProperty,
        List<MappingError> errors)
    {
        var relationships = new List<RelationshipMap>(relationshipSpecs.Count);
        var ownerKeyCount = byProperty.Values.Count(p => p.IsKey);
        foreach (var spec in relationshipSpecs)
        {
            var target = $"{entityType.Name}.{spec.PropertyName}";

            // Composite keys need composite foreign keys (ADR-0019 add.1): the FK
            // list pairs with the referenced side's key parts, in key order, so
            // the counts must agree wherever the key shape is known.
            switch (spec.Kind)
            {
                case RelationshipKind.ManyToOne:
                    var unmapped = spec.ForeignKeyProperties.Where(n => !byProperty.ContainsKey(n)).ToArray();
                    if (unmapped.Length > 0)
                    {
                        errors.Add(new MappingError(
                            "MAP-016", target,
                            $"[ManyToOne] names foreign-key propert{(unmapped.Length == 1 ? "y" : "ies")} "
                            + $"'{string.Join("', '", unmapped)}' that are not mapped properties"));
                        continue;
                    }

                    if (KeyArity(spec.TargetType) is { } targetArity && targetArity != spec.ForeignKeyProperties.Count)
                    {
                        errors.Add(new MappingError(
                            "MAP-016", target,
                            $"[ManyToOne] declares {spec.ForeignKeyProperties.Count} foreign-key propert{(spec.ForeignKeyProperties.Count == 1 ? "y" : "ies")} "
                            + $"but '{spec.TargetType.Name}' has a {targetArity}-part key"));
                        continue;
                    }

                    break;

                case RelationshipKind.OneToMany:
                case RelationshipKind.OneToOne:
                    if (ownerKeyCount > 0 && spec.ForeignKeyProperties.Count != ownerKeyCount)
                    {
                        errors.Add(new MappingError(
                            "MAP-021", target,
                            $"declares {spec.ForeignKeyProperties.Count} target foreign-key propert{(spec.ForeignKeyProperties.Count == 1 ? "y" : "ies")} "
                            + $"but this entity has a {ownerKeyCount}-part key"));
                        continue;
                    }

                    break;

                default:
                    if (ownerKeyCount > 0 && spec.LinkForeignKeysToOwner.Count != ownerKeyCount)
                    {
                        errors.Add(new MappingError(
                            "MAP-022", target,
                            $"link '{spec.LinkType!.Name}' declares {spec.LinkForeignKeysToOwner.Count} [ForeignKey] "
                            + $"propert{(spec.LinkForeignKeysToOwner.Count == 1 ? "y" : "ies")} referencing this type, whose key has {ownerKeyCount} part(s)"));
                        continue;
                    }

                    if (KeyArity(spec.TargetType) is { } elementArity && spec.LinkForeignKeysToTarget.Count != elementArity)
                    {
                        errors.Add(new MappingError(
                            "MAP-022", target,
                            $"link '{spec.LinkType!.Name}' declares {spec.LinkForeignKeysToTarget.Count} [ForeignKey] "
                            + $"propert{(spec.LinkForeignKeysToTarget.Count == 1 ? "y" : "ies")} referencing '{spec.TargetType.Name}', whose key has {elementArity} part(s)"));
                        continue;
                    }

                    break;
            }

            relationships.Add(new RelationshipMap(
                spec.PropertyName, spec.Kind, spec.TargetType, spec.ForeignKeyProperties,
                spec.LinkType, spec.LinkForeignKeysToOwner, spec.LinkForeignKeysToTarget));
        }

        return relationships;
    }

    /// <summary>
    /// The related type's key arity by its [Key] declarations — null when it
    /// declares none (convention-mapped or unknown: the check is skipped rather
    /// than guessed).
    /// </summary>
    private static int? KeyArity(Type entityType)
    {
        var count = entityType
            .GetProperties(BindingFlags.Public | BindingFlags.Instance)
            .Count(p => p.GetCustomAttribute<KeyAttribute>() is not null);
        return count > 0 ? count : null;
    }
}
