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
        foreach (var spec in relationshipSpecs)
        {
            if (!byProperty.ContainsKey(spec.ForeignKeyProperty))
            {
                errors.Add(new MappingError(
                    "MAP-016",
                    $"{entityType.Name}.{spec.PropertyName}",
                    $"[ManyToOne] names foreign-key property '{spec.ForeignKeyProperty}', which is not a mapped property"));
                continue;
            }

            relationships.Add(new RelationshipMap(spec.PropertyName, spec.TargetType, spec.ForeignKeyProperty));
        }

        return relationships;
    }
}
