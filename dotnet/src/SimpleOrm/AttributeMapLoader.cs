using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// The attribute loader: enforces the opt-in mapping rules of ADR-0004..0008 and
/// produces an <see cref="EntityMap"/>. Collects every violation before throwing.
/// </summary>
internal static class AttributeMapLoader
{
    public static EntityMap Load(Type entityType, INamingConvention convention)
    {
        var errors = new List<MappingError>();

        var (kind, relationName, schema, statementSql, statementParameters) =
            ReadRelationSource(entityType, convention, errors);

        var specs = new List<MappedPropertySpec>();
        var relationships = new List<RelationshipSpec>();
        ReadProperties(entityType, kind, specs, relationships, errors);

        var indexSpecs = ReadIndexes(entityType, kind, errors);
        if (statementSql is not null)
        {
            // For views the declared list is empty, so any placeholder in the
            // defining SELECT is PRM-010 — view definitions take no parameters.
            CheckStatementPlaceholders(entityType, kind, statementSql, statementParameters, errors);
        }

        var map = MapAssembler.Assemble(
            entityType, kind, relationName, schema, statementSql, statementParameters,
            specs, indexSpecs, relationships, convention, errors);

        if (map is null)
        {
            throw new MappingException(entityType, errors);
        }

        return map;
    }

    private static (RelationKind Kind, string? Name, string? Schema, string? Sql, IReadOnlyList<StatementParameter> Parameters)
        ReadRelationSource(Type entityType, INamingConvention convention, List<MappingError> errors)
    {
        var sources = new List<(RelationKind Kind, string? Name, string? Schema, string? Sql, object[]? RawParameters)>();

        if (entityType.GetCustomAttribute<TableAttribute>() is { } table)
        {
            sources.Add((RelationKind.Table, table.Name, table.Schema, null, null));
        }

        if (entityType.GetCustomAttribute<ViewAttribute>() is { } view)
        {
            sources.Add((RelationKind.View, view.Name, view.Schema, view.Sql, null));
        }

        if (entityType.GetCustomAttribute<MaterializedViewAttribute>() is { } materialized)
        {
            sources.Add((RelationKind.MaterializedView, materialized.Name, materialized.Schema, materialized.Sql, null));
        }

        if (entityType.GetCustomAttribute<StatementAttribute>() is { } statement)
        {
            sources.Add((RelationKind.Statement, null, null, statement.Sql, statement.Parameters));
        }

        if (entityType.GetCustomAttribute<ProcedureAttribute>() is { } procedure)
        {
            sources.Add((RelationKind.Procedure, procedure.Name, procedure.Schema, procedure.Sql, procedure.Parameters));
        }

        if (sources.Count > 1)
        {
            errors.Add(new MappingError(
                "MAP-012",
                entityType.Name,
                $"carries {sources.Count} relation sources; exactly one of [Table]/[View]/[MaterializedView]/[Statement]/[Procedure] is allowed"));
        }

        if (sources.Count == 0)
        {
            // Property attributes without a source attribute: a table named by convention.
            return (RelationKind.Table, convention.TableName(entityType.Name), null, null, []);
        }

        var source = sources[0];
        var parameters = source.Kind is RelationKind.Statement or RelationKind.Procedure
            ? ParseStatementParameters(entityType, source.RawParameters!, errors)
            : [];

        if (source.Kind != RelationKind.Table && string.IsNullOrWhiteSpace(source.Sql))
        {
            errors.Add(new MappingError("MAP-019", entityType.Name, $"the {source.Kind} defining SQL is empty"));
        }

        return (source.Kind, source.Name, source.Schema, source.Sql, parameters);
    }

    private static IReadOnlyList<StatementParameter> ParseStatementParameters(
        Type entityType, object[] tokens, List<MappingError> errors)
    {
        var target = $"{entityType.Name} [Statement]";
        if (tokens.Length % 2 != 0)
        {
            errors.Add(new MappingError(
                "MAP-017", target, $"parameter tokens must be (name, type) pairs; found {tokens.Length} tokens"));
            return [];
        }

        var parameters = new List<StatementParameter>(tokens.Length / 2);
        var seen = new HashSet<string>();
        for (var i = 0; i < tokens.Length; i += 2)
        {
            if (tokens[i] is not string name || tokens[i + 1] is not Type type)
            {
                errors.Add(new MappingError(
                    "MAP-017", target,
                    $"token pair {i / 2} must be a name string followed by a Type; found ({Describe(tokens[i])}, {Describe(tokens[i + 1])})"));
                continue;
            }

            if (!seen.Add(name))
            {
                errors.Add(new MappingError("MAP-017", target, $"duplicate parameter name '{name}'"));
                continue;
            }

            parameters.Add(new StatementParameter(name, type));
        }

        return parameters;
    }

    private static void CheckStatementPlaceholders(
        Type entityType, RelationKind kind, string sql, IReadOnlyList<StatementParameter> declared, List<MappingError> errors)
    {
        var target = $"{entityType.Name} [{kind}]";
        var placeholders = SqlPlaceholders.Find(sql);
        foreach (var placeholder in placeholders)
        {
            if (declared.All(p => p.Name != placeholder))
            {
                errors.Add(new MappingError(
                    "PRM-010", target, $"SQL uses @{placeholder}, which is not declared in the attribute"));
            }
        }

        foreach (var parameter in declared)
        {
            if (!placeholders.Contains(parameter.Name))
            {
                errors.Add(new MappingError(
                    "PRM-011", target, $"declared parameter '{parameter.Name}' is never used by the SQL"));
            }
        }
    }

    private static void ReadProperties(
        Type entityType,
        RelationKind kind,
        List<MappedPropertySpec> specs,
        List<RelationshipSpec> relationships,
        List<MappingError> errors)
    {
        foreach (var property in PropertiesInDeclarationOrder(entityType))
        {
            var target = $"{entityType.Name}.{property.Name}";
            var column = property.GetCustomAttribute<ColumnAttribute>();
            var ignore = property.GetCustomAttribute<IgnoreAttribute>();
            var manyToOne = property.GetCustomAttribute<ManyToOneAttribute>();
            var oneToMany = property.GetCustomAttribute<OneToManyAttribute>();
            var manyToMany = property.GetCustomAttribute<ManyToManyAttribute>();
            var key = property.GetCustomAttribute<KeyAttribute>();
            var generated = property.GetCustomAttribute<GeneratedAttribute>();
            var version = property.GetCustomAttribute<VersionAttribute>();
            var enumAsInt = property.GetCustomAttribute<EnumAsIntAttribute>();
            var foreignKey = property.GetCustomAttribute<ForeignKeyAttribute>();

            if (manyToOne is not null || oneToMany is not null || manyToMany is not null)
            {
                var navigationCount = (manyToOne is null ? 0 : 1) + (oneToMany is null ? 0 : 1) + (manyToMany is null ? 0 : 1);
                if (navigationCount > 1)
                {
                    errors.Add(new MappingError(
                        "MAP-019", target, "a property carries at most one relationship attribute"));
                    continue;
                }

                if (column is not null || ignore is not null)
                {
                    errors.Add(new MappingError(
                        "MAP-019", target, "a navigation cannot combine with [Column] or [Ignore]"));
                }

                if (property.SetMethod is { IsPublic: true })
                {
                    errors.Add(new MappingError(
                        "MAP-011", target,
                        "a navigation must not expose a public setter; declare it { get; private set; }"));
                }

                if (manyToOne is not null)
                {
                    relationships.Add(new RelationshipSpec(
                        property.Name, RelationshipKind.ManyToOne, property.PropertyType, manyToOne.ForeignKeyProperty));
                }
                else
                {
                    ReadCollectionNavigation(entityType, property, target, oneToMany, manyToMany, relationships, errors);
                }

                continue;
            }

            if (ignore is not null)
            {
                if (column is not null)
                {
                    errors.Add(new MappingError("MAP-019", target, "[Ignore] cannot combine with [Column]"));
                }

                continue;
            }

            if (column is null)
            {
                if (key is not null || generated is not null || version is not null
                    || enumAsInt is not null || foreignKey is not null)
                {
                    errors.Add(new MappingError(
                        "MAP-019", target, "mapping attributes require [Column] on the same property"));
                }
                else if (property.GetMethod is { IsPublic: true } && property.SetMethod is { IsPublic: true })
                {
                    errors.Add(new MappingError(
                        "MAP-010", target,
                        "a public settable property must carry [Column], [Ignore], or a relationship attribute (ADR-0004)"));
                }

                continue;
            }

            if (enumAsInt is not null && !property.PropertyType.IsEnum
                && Nullable.GetUnderlyingType(property.PropertyType)?.IsEnum != true)
            {
                errors.Add(new MappingError("MAP-019", target, "[EnumAsInt] requires an enum property"));
            }

            if (kind != RelationKind.Table && (generated is not null || version is not null))
            {
                errors.Add(new MappingError(
                    "MAP-013", target, $"[Generated]/[Version] are only valid on a table-backed entity, not a {kind}"));
            }

            if (key is not null && kind is RelationKind.Statement or RelationKind.Procedure)
            {
                errors.Add(new MappingError(
                    "MAP-013", target, $"[Key] is not valid on a {kind}-backed entity"));
            }

            specs.Add(new MappedPropertySpec(property)
            {
                ExplicitColumn = column.Name,
                IsKey = key is not null,
                IsGenerated = generated is not null,
                IsVersion = version is not null,
                EnumAsInt = enumAsInt is not null,
                ForeignKeyReferences = foreignKey?.References,
            });
        }
    }

    private static IReadOnlyList<IndexSpec> ReadIndexes(Type entityType, RelationKind kind, List<MappingError> errors)
    {
        var attributes = entityType.GetCustomAttributes<IndexAttribute>().ToArray();
        if (attributes.Length == 0)
        {
            return [];
        }

        if (kind is not (RelationKind.Table or RelationKind.MaterializedView))
        {
            errors.Add(new MappingError(
                "MAP-014", entityType.Name,
                $"[Index] is only valid on tables and materialized views, not a {kind}"));
            return [];
        }

        var specs = new List<IndexSpec>(attributes.Length);
        foreach (var attribute in attributes)
        {
            var target = $"{entityType.Name} [Index]";
            var columns = new List<(string PropertyName, bool Descending)>();
            var valid = true;

            foreach (var token in attribute.Columns)
            {
                switch (token)
                {
                    case string propertyName:
                        columns.Add((propertyName, false));
                        break;
                    case SortOrder order when columns.Count == 0:
                        errors.Add(new MappingError(
                            "MAP-015", target, $"SortOrder.{order} has no preceding column to apply to"));
                        valid = false;
                        break;
                    case SortOrder order:
                        var (name, _) = columns[columns.Count - 1];
                        columns[columns.Count - 1] = (name, order == SortOrder.Desc);
                        break;
                    default:
                        errors.Add(new MappingError(
                            "MAP-015", target,
                            $"token '{Describe(token)}' is neither a property name string nor a SortOrder"));
                        valid = false;
                        break;
                }
            }

            // Detect doubled SortOrder tokens (two orders in a row).
            for (var i = 1; i < attribute.Columns.Length; i++)
            {
                if (attribute.Columns[i] is SortOrder && attribute.Columns[i - 1] is SortOrder)
                {
                    errors.Add(new MappingError(
                        "MAP-015", target, "two consecutive SortOrder tokens; each applies to the column before it"));
                    valid = false;
                }
            }

            if (columns.Count == 0)
            {
                errors.Add(new MappingError("MAP-015", target, "the column list is empty"));
                valid = false;
            }

            if (valid)
            {
                specs.Add(new IndexSpec(attribute.Name, attribute.Unique, columns));
            }
        }

        return specs;
    }

    /// <summary>
    /// Public instance properties, most-derived class first, declaration order within
    /// each class (metadata token order) — so a base model's audit columns come last,
    /// matching the natural table layout.
    /// </summary>
    internal static IEnumerable<PropertyInfo> PropertiesInDeclarationOrder(Type entityType)
    {
        for (var type = entityType; type is not null && type != typeof(object); type = type.BaseType)
        {
            var declared = type
                .GetProperties(BindingFlags.Public | BindingFlags.Instance | BindingFlags.DeclaredOnly)
                .Where(p => p.GetIndexParameters().Length == 0)
                .OrderBy(p => p.MetadataToken);
            foreach (var property in declared)
            {
                yield return property;
            }
        }
    }

    internal static bool HasMappingAttributes(Type entityType)
    {
        if (entityType.GetCustomAttributes()
            .Any(a => a is TableAttribute or ViewAttribute or MaterializedViewAttribute
                or StatementAttribute or ProcedureAttribute or IndexAttribute))
        {
            return true;
        }

        return PropertiesInDeclarationOrder(entityType).Any(p => p.GetCustomAttributes()
            .Any(a => a is ColumnAttribute or IgnoreAttribute or KeyAttribute or GeneratedAttribute
                or VersionAttribute or EnumAsIntAttribute or ForeignKeyAttribute or ManyToOneAttribute
                or OneToManyAttribute or ManyToManyAttribute));
    }

    /// <summary>
    /// Resolves a [OneToMany]/[ManyToMany] collection navigation (ADR-0019): the
    /// element type from the property's IEnumerable&lt;T&gt; (MAP-020); a
    /// [OneToMany] target FK that exists on the element type (MAP-021); a
    /// [ManyToMany] link whose [ForeignKey] declarations reference each side
    /// exactly once (MAP-022).
    /// </summary>
    private static void ReadCollectionNavigation(
        Type entityType,
        PropertyInfo property,
        string target,
        OneToManyAttribute? oneToMany,
        ManyToManyAttribute? manyToMany,
        List<RelationshipSpec> relationships,
        List<MappingError> errors)
    {
        var elementType = CollectionElementType(property.PropertyType);
        if (elementType is null)
        {
            errors.Add(new MappingError(
                "MAP-020", target,
                "a collection navigation must be a generic collection (IEnumerable<T>) of an entity type"));
            return;
        }

        if (oneToMany is not null)
        {
            if (elementType.GetProperty(oneToMany.TargetForeignKeyProperty) is null)
            {
                errors.Add(new MappingError(
                    "MAP-021", target,
                    $"[OneToMany] names foreign-key property '{oneToMany.TargetForeignKeyProperty}', which does not exist on '{elementType.Name}'"));
                return;
            }

            relationships.Add(new RelationshipSpec(
                property.Name, RelationshipKind.OneToMany, elementType, oneToMany.TargetForeignKeyProperty));
            return;
        }

        var link = manyToMany!.Through;
        var toOwner = LinkForeignKey(link, entityType, target, "this type", errors);
        var toTarget = LinkForeignKey(link, elementType, target, $"'{elementType.Name}'", errors);
        if (toOwner is null || toTarget is null)
        {
            return;
        }

        relationships.Add(new RelationshipSpec(
            property.Name, RelationshipKind.ManyToMany, elementType, foreignKeyProperty: null)
        {
            LinkType = link,
            LinkForeignKeyToOwner = toOwner,
            LinkForeignKeyToTarget = toTarget,
        });
    }

    /// <summary>The single link property carrying [ForeignKey(referenced)] — missing or ambiguous is MAP-022.</summary>
    private static string? LinkForeignKey(
        Type link, Type referenced, string target, string side, List<MappingError> errors)
    {
        var candidates = link
            .GetProperties(BindingFlags.Public | BindingFlags.Instance)
            .Where(p => p.GetCustomAttribute<ForeignKeyAttribute>()?.References == referenced)
            .ToArray();
        if (candidates.Length == 1)
        {
            return candidates[0].Name;
        }

        errors.Add(new MappingError(
            "MAP-022", target,
            candidates.Length == 0
                ? $"[ManyToMany] link '{link.Name}' has no [ForeignKey] property referencing {side}"
                : $"[ManyToMany] link '{link.Name}' has {candidates.Length} [ForeignKey] properties referencing {side}; exactly one is required"));
        return null;
    }

    /// <summary>The entity element type of a collection property, or null (string and byte[] are not collections of entities).</summary>
    private static Type? CollectionElementType(Type propertyType)
    {
        var enumerable = propertyType.IsInterface && propertyType.IsGenericType
                && propertyType.GetGenericTypeDefinition() == typeof(IEnumerable<>)
            ? propertyType
            : propertyType.GetInterfaces().FirstOrDefault(i =>
                i.IsGenericType && i.GetGenericTypeDefinition() == typeof(IEnumerable<>));
        var element = enumerable?.GetGenericArguments()[0];
        return element is { IsClass: true } && element != typeof(string) ? element : null;
    }

    private static string Describe(object? token) => token switch
    {
        null => "null",
        string s => $"\"{s}\"",
        Type t => $"typeof({t.Name})",
        _ => $"{token.GetType().Name} '{token}'",
    };
}
