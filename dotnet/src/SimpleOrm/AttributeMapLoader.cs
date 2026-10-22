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
        if (IsOwnedType(entityType))
        {
            // An owned value type (ADR-0030) has no map of its own; it is read
            // through its owner. Loading it directly is a caller error.
            throw new MappingException(entityType, [new MappingError(
                "MAP-024", entityType.Name,
                "is an [Owned] value type, not an entity; it maps only as a member of its owner")]);
        }

        var (kind, relationName, schema, statementSql, statementParameters) =
            ReadRelationSource(entityType, convention, errors);

        var specs = new List<MappedPropertySpec>();
        var relationships = new List<RelationshipSpec>();
        ReadProperties(entityType, kind, specs, relationships, errors);

        var indexSpecs = ReadIndexes(entityType, kind, errors);
        var triggerSpecs = ReadTriggers(entityType, kind, errors);
        if (statementSql is not null)
        {
            // For views the declared list is empty, so any placeholder in the
            // defining SELECT is PRM-010 — view definitions take no parameters.
            CheckStatementPlaceholders(entityType, kind, statementSql, statementParameters, errors);
        }

        var map = MapAssembler.Assemble(
            entityType, kind, relationName, schema, statementSql, statementParameters,
            specs, indexSpecs, relationships, convention, errors, triggerSpecs);

        if (map is null)
        {
            throw new MappingException(entityType, errors);
        }

        return map;
    }

    // --- triggers (ADR-0042) ---------------------------------------------------------

    /// <summary>A class carrying the declaration form of <c>[Trigger]</c>: a declaration object, never an entity — scans skip it.</summary>
    internal static bool IsTriggerType(Type type)
        => type.GetCustomAttributes<TriggerAttribute>(inherit: false).Any(a => a.Name is not null);

    /// <summary>
    /// The links an entity carries — <c>[Trigger(typeof(X))]</c>, read without
    /// inheritance — each resolved to its class's declaration. Placement rules
    /// (<c>MAP-029</c>): tables take every timing, views only <c>InsteadOf</c>, no
    /// other source takes a trigger; column resolution happens at assembly.
    /// </summary>
    private static IReadOnlyList<TriggerSpec> ReadTriggers(Type entityType, RelationKind kind, List<MappingError> errors)
    {
        var links = entityType.GetCustomAttributes<TriggerAttribute>(inherit: false).ToArray();
        if (links.Length == 0)
        {
            return [];
        }

        var specs = new List<TriggerSpec>();
        foreach (var link in links)
        {
            var target = $"{entityType.Name} [Trigger]";
            if (link.Trigger is null)
            {
                errors.Add(new MappingError(
                    "MAP-029", target,
                    $"an entity links a trigger class — [Trigger(typeof(X))]; the declaration form belongs on the trigger class itself (found '{link.Name}')"));
                continue;
            }

            if (kind is not (RelationKind.Table or RelationKind.View))
            {
                errors.Add(new MappingError(
                    "MAP-029", target, $"a trigger is only valid on a table (any timing) or a view (INSTEAD OF), not a {kind}"));
                continue;
            }

            var spec = ReadTriggerDeclaration(link.Trigger, $"{entityType.Name} [Trigger(typeof({link.Trigger.Name}))]", errors);
            if (spec is null)
            {
                continue;
            }

            if (kind == RelationKind.View && spec.Timing != TriggerTiming.InsteadOf)
            {
                errors.Add(new MappingError(
                    "MAP-029", target, $"'{spec.Name}' is {spec.Timing} on a view; a view takes only INSTEAD OF triggers"));
                continue;
            }

            if (specs.Any(s => s.TriggerType == spec.TriggerType))
            {
                errors.Add(new MappingError("MAP-029", target, $"'{link.Trigger.Name}' is linked twice"));
                continue;
            }

            specs.Add(spec);
        }

        return specs;
    }

    /// <summary>
    /// Reads a trigger class's declaration — exactly one <c>[Trigger(name, timing,
    /// events, sql)]</c> on a class that is neither an entity nor an owned type —
    /// with the shape checks that need no entity: a name, at least one event, a
    /// body, <c>UpdateOf</c> only with the update event, well-formed tokens that
    /// are possible for the events (<c>{old}</c> needs update or delete,
    /// <c>{new}</c> insert or update). Column resolution waits for the entity.
    /// </summary>
    internal static TriggerSpec? ReadTriggerDeclaration(Type triggerType, string target, List<MappingError> errors)
    {
        var declarations = triggerType.GetCustomAttributes<TriggerAttribute>(inherit: false).ToArray();
        if (declarations.Any(d => d.Trigger is not null))
        {
            errors.Add(new MappingError(
                "MAP-029", target, $"'{triggerType.Name}' links a trigger of its own; a trigger class declares, it never links"));
            return null;
        }

        if (declarations.Length != 1)
        {
            errors.Add(new MappingError(
                "MAP-029", target,
                declarations.Length == 0
                    ? $"'{triggerType.Name}' carries no [Trigger(name, timing, events, sql)] declaration"
                    : $"'{triggerType.Name}' carries {declarations.Length} declarations; a trigger class declares exactly one trigger"));
            return null;
        }

        if (IsOwnedType(triggerType) || HasMappingAttributes(triggerType))
        {
            errors.Add(new MappingError(
                "MAP-029", target, $"'{triggerType.Name}' is an entity or an owned type; a trigger class is a declaration object with no columns"));
            return null;
        }

        var declaration = declarations[0];
        var valid = true;
        if (string.IsNullOrWhiteSpace(declaration.Name))
        {
            errors.Add(new MappingError("MAP-029", target, $"'{triggerType.Name}' declares a blank trigger name"));
            valid = false;
        }

        if (declaration.Events == TriggerEvent.None || (declaration.Events & ~(TriggerEvent.Insert | TriggerEvent.Update | TriggerEvent.Delete)) != 0)
        {
            errors.Add(new MappingError("MAP-029", target, $"'{triggerType.Name}' must declare at least one of Insert, Update, Delete"));
            valid = false;
        }

        if (!Enum.IsDefined(typeof(TriggerTiming), declaration.Timing))
        {
            errors.Add(new MappingError("MAP-029", target, $"'{triggerType.Name}' declares an unknown timing"));
            valid = false;
        }

        if (string.IsNullOrWhiteSpace(declaration.Sql))
        {
            errors.Add(new MappingError("MAP-029", target, $"'{triggerType.Name}' declares a blank body"));
            valid = false;
        }

        var updateOf = declaration.UpdateOf ?? [];
        if (updateOf.Length > 0 && !declaration.Events.HasFlag(TriggerEvent.Update))
        {
            errors.Add(new MappingError("MAP-029", target, $"'{triggerType.Name}' declares UpdateOf without the Update event"));
            valid = false;
        }

        if (updateOf.Any(string.IsNullOrWhiteSpace))
        {
            errors.Add(new MappingError("MAP-029", target, $"'{triggerType.Name}' declares a blank UpdateOf entry"));
            valid = false;
        }

        if (!valid)
        {
            return null;
        }

        var before = errors.Count;
        var segments = TriggerTokens.Parse(declaration.Sql!, target, errors);
        if (errors.Count > before)
        {
            return null;
        }

        // The body is shared by every declared event, so a row token must have rows on
        // each of them: {new} needs every event to be insert or update, {old} update or delete.
        var newImpossible = declaration.Events.HasFlag(TriggerEvent.Delete);
        var oldImpossible = declaration.Events.HasFlag(TriggerEvent.Insert);
        foreach (var segment in segments.Where(s => s.Kind is "new" or "old"))
        {
            if (segment.Kind == "new" && newImpossible)
            {
                errors.Add(new MappingError("MAP-029", target, "{new: …} has no rows on the delete event; a body is shared by every declared event"));
                valid = false;
            }

            if (segment.Kind == "old" && oldImpossible)
            {
                errors.Add(new MappingError("MAP-029", target, "{old: …} has no rows on the insert event; a body is shared by every declared event"));
                valid = false;
            }
        }

        return valid
            ? new TriggerSpec(triggerType, declaration.Name!.Trim(), declaration.Timing, declaration.Events, declaration.Sql!, updateOf)
            : null;
    }

    /// <summary>
    /// The scan rule for triggers (<c>MAP-029</c>): across the scanned entities, a
    /// trigger class linked by two entities (a trigger belongs to one table) or
    /// two classes declaring the same trigger name (names are database-wide on
    /// SQLite and SQL Server). A lone load cannot see either; startup validation
    /// and the CLI check the set as a whole, like <c>MAP-028</c>.
    /// </summary>
    internal static IReadOnlyList<MappingError> TriggerConflicts(IEnumerable<Type> types)
    {
        var errors = new List<MappingError>();
        var linkedBy = new Dictionary<Type, List<string>>();
        var namedBy = new Dictionary<string, List<string>>(StringComparer.OrdinalIgnoreCase);

        // Every declaration class counts for the name rule — the linked ones and any the scan sees unlinked
        // (a class kept for its removal step still names the database object).
        void Declares(Type triggerType)
        {
            var name = triggerType.GetCustomAttributes<TriggerAttribute>(inherit: false)
                .Select(a => a.Name?.Trim()).FirstOrDefault(n => !string.IsNullOrEmpty(n));
            if (name is null)
            {
                return;
            }

            if (!namedBy.TryGetValue(name, out var classes))
            {
                namedBy[name] = classes = [];
            }

            if (!classes.Contains(triggerType.Name))
            {
                classes.Add(triggerType.Name);
            }
        }

        foreach (var type in types.Where(t => t is { IsClass: true, IsAbstract: false } && !IsOwnedType(t)))
        {
            if (IsTriggerType(type))
            {
                Declares(type);
                continue;
            }

            foreach (var link in type.GetCustomAttributes<TriggerAttribute>(inherit: false).Where(a => a.Trigger is not null))
            {
                if (!linkedBy.TryGetValue(link.Trigger!, out var owners))
                {
                    linkedBy[link.Trigger!] = owners = [];
                }

                if (!owners.Contains(type.Name))
                {
                    owners.Add(type.Name);   // a class linked twice by one entity is that entity's own MAP-029, not a scan conflict
                }

                Declares(link.Trigger!);
            }
        }

        foreach (var pair in linkedBy.Where(p => p.Value.Count > 1).OrderBy(p => p.Key.Name, StringComparer.Ordinal))
        {
            errors.Add(new MappingError(
                "MAP-029", pair.Key.Name,
                $"is linked by '{string.Join("', '", pair.Value.OrderBy(n => n, StringComparer.Ordinal))}': a trigger belongs to one table — declare one class per table"));
        }

        foreach (var pair in namedBy.Where(p => p.Value.Count > 1).OrderBy(p => p.Key, StringComparer.Ordinal))
        {
            errors.Add(new MappingError(
                "MAP-029", pair.Key,
                $"is declared by '{string.Join("', '", pair.Value.OrderBy(n => n, StringComparer.Ordinal))}': trigger names are unique across the model"));
        }

        return errors;
    }

    private static (RelationKind Kind, string? Name, string? Schema, string? Sql, IReadOnlyList<StatementParameter> Parameters)
        ReadRelationSource(Type entityType, INamingConvention convention, List<MappingError> errors)
    {
        var sources = new List<(RelationKind Kind, string? Name, string? Schema, string? Sql, object[]? RawParameters)>();

        // Class-level attributes are read without inheritance (ADR-0041): a subclass
        // never adopts its base's relation source — a base carrying one is MAP-028 below.
        if (entityType.GetCustomAttribute<TableAttribute>(inherit: false) is { } table)
        {
            sources.Add((RelationKind.Table, table.Name, table.Schema, null, null));
        }

        if (entityType.GetCustomAttribute<ViewAttribute>(inherit: false) is { } view)
        {
            sources.Add((RelationKind.View, view.Name, view.Schema, view.Sql, null));
        }

        if (entityType.GetCustomAttribute<MaterializedViewAttribute>(inherit: false) is { } materialized)
        {
            sources.Add((RelationKind.MaterializedView, materialized.Name, materialized.Schema, materialized.Sql, null));
        }

        if (entityType.GetCustomAttribute<StatementAttribute>(inherit: false) is { } statement)
        {
            sources.Add((RelationKind.Statement, null, null, statement.Sql, statement.Parameters));
        }

        if (entityType.GetCustomAttribute<ProcedureAttribute>(inherit: false) is { } procedure)
        {
            sources.Add((RelationKind.Procedure, procedure.Name, procedure.Schema, procedure.Sql, procedure.Parameters));
        }

        CheckBaseClasses(entityType, errors);

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
            var oneToOne = property.GetCustomAttribute<OneToOneAttribute>();
            var oneToMany = property.GetCustomAttribute<OneToManyAttribute>();
            var manyToMany = property.GetCustomAttribute<ManyToManyAttribute>();
            var key = property.GetCustomAttribute<KeyAttribute>();
            var generated = property.GetCustomAttribute<GeneratedAttribute>();
            var version = property.GetCustomAttribute<VersionAttribute>();
            var enumAsInt = property.GetCustomAttribute<EnumAsIntAttribute>();
            var foreignKey = property.GetCustomAttribute<ForeignKeyAttribute>();
            var owned = property.GetCustomAttribute<OwnedAttribute>();
            var cascade = property.GetCustomAttribute<CascadeAttribute>();
            var createdAt = property.GetCustomAttribute<CreatedAtAttribute>();
            var updatedAt = property.GetCustomAttribute<UpdatedAtAttribute>();
            var softDelete = property.GetCustomAttribute<SoftDeleteAttribute>();
            var renamedFrom = property.GetCustomAttribute<RenamedFromAttribute>();

            if (owned is not null)
            {
                if (column is not null || ignore is not null || key is not null || generated is not null
                    || version is not null || enumAsInt is not null || foreignKey is not null
                    || manyToOne is not null || oneToOne is not null || oneToMany is not null || manyToMany is not null)
                {
                    errors.Add(new MappingError(
                        "MAP-019", target, "[Owned] cannot combine with any other mapping attribute"));
                }
                else
                {
                    ReadOwnedType(property, target, owned, specs, errors);
                }

                continue;
            }

            if (manyToOne is not null || oneToOne is not null || oneToMany is not null || manyToMany is not null)
            {
                var navigationCount = (manyToOne is null ? 0 : 1) + (oneToOne is null ? 0 : 1)
                    + (oneToMany is null ? 0 : 1) + (manyToMany is null ? 0 : 1);
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
                    if (manyToOne.ForeignKeyProperties.Count == 0)
                    {
                        errors.Add(new MappingError(
                            "MAP-016", target, "[ManyToOne] needs at least one foreign-key property"));
                    }
                    else
                    {
                        relationships.Add(new RelationshipSpec(
                            property.Name, RelationshipKind.ManyToOne, property.PropertyType,
                            manyToOne.ForeignKeyProperties));
                    }
                }
                else if (oneToOne is not null)
                {
                    ReadOneToOneNavigation(property, target, oneToOne, relationships, errors);
                }
                else
                {
                    ReadCollectionNavigation(entityType, property, target, oneToMany, manyToMany, relationships, errors);
                }

                ApplyCascade(cascade, property.Name, target, manyToMany is not null, relationships, errors);
                continue;
            }

            if (cascade is not null)
            {
                errors.Add(new MappingError(
                    "MAP-025", target, "[Cascade] belongs on a [ManyToOne], [OneToOne], or [OneToMany] navigation"));
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
                if (renamedFrom is not null)
                {
                    errors.Add(new MappingError("MAP-027", target, "[RenamedFrom] belongs on a [Column] property"));
                }

                if (createdAt is not null || updatedAt is not null || softDelete is not null)
                {
                    errors.Add(new MappingError(
                        "MAP-026", target, "a hook belongs on a [Column] property, never on a navigation, an [Ignore]d or unmapped property"));
                }
                else if (key is not null || generated is not null || version is not null
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

            if (createdAt is not null && updatedAt is not null)
            {
                // Two audit roles collapse into one spec field; the assembler could not see the conflict.
                errors.Add(new MappingError("MAP-026", target, "a property carries at most one of [CreatedAt], [UpdatedAt], [SoftDelete]"));
            }

            if (renamedFrom is not null && string.IsNullOrWhiteSpace(renamedFrom.PreviousColumn))
            {
                errors.Add(new MappingError("MAP-027", target, "[RenamedFrom] needs the previous column's name"));
            }

            specs.Add(new MappedPropertySpec(property)
            {
                ExplicitColumn = column.Name,
                RenamedFrom = renamedFrom?.PreviousColumn.Trim(),
                IsKey = key is not null,
                IsGenerated = generated is not null,
                IsVersion = version is not null,
                EnumAsInt = enumAsInt is not null,
                ForeignKeyReferences = foreignKey?.References,
                Audit = createdAt is not null ? AuditRole.CreatedAt : updatedAt is not null ? AuditRole.UpdatedAt : AuditRole.None,
                IsSoftDelete = softDelete is not null,
            });
        }
    }

    /// <summary>
    /// Reads an [Owned] value type (ADR-0030): a class with a public parameterless
    /// constructor and a settable navigation, whose [Column] members flatten into
    /// the owner under the navigation's prefix. The owned type is not an entity —
    /// a relation source, [Index], key, version, generated column, foreign key,
    /// relationship, or nested [Owned] inside it is <c>MAP-024</c>; the opt-in
    /// rule (<c>MAP-010</c>) applies to its members exactly as to an entity's.
    /// </summary>
    private static void ReadOwnedType(
        PropertyInfo navigation,
        string target,
        OwnedAttribute owned,
        List<MappedPropertySpec> specs,
        List<MappingError> errors)
    {
        var ownedType = navigation.PropertyType;
        if (!ownedType.IsClass || ownedType == typeof(string) || ownedType == typeof(byte[])
            || typeof(System.Collections.IEnumerable).IsAssignableFrom(ownedType))
        {
            errors.Add(new MappingError(
                "MAP-024", target,
                "an [Owned] navigation must be a single class-typed value; collections and scalars cannot be owned"));
            return;
        }

        if (navigation.SetMethod is null)
        {
            errors.Add(new MappingError(
                "MAP-024", target, "an [Owned] navigation needs a setter (public or init) so the mapper can assign it"));
            return;
        }

        if (ownedType.GetCustomAttributes(inherit: false).Any(a => a is TableAttribute or ViewAttribute or MaterializedViewAttribute
                or StatementAttribute or ProcedureAttribute or IndexAttribute))
        {
            errors.Add(new MappingError(
                "MAP-024", target,
                $"'{ownedType.Name}' is an entity (it carries a relation source or [Index]); an owned type has no table of its own"));
            return;
        }

        if (!IsOwnedType(ownedType))
        {
            errors.Add(new MappingError(
                "MAP-024", target,
                $"'{ownedType.Name}' must itself be declared [Owned] at class level — that is what keeps it out of the entity set"));
            return;
        }

        if (ownedType.GetConstructor(Type.EmptyTypes) is null)
        {
            errors.Add(new MappingError(
                "MAP-024", target, $"'{ownedType.Name}' needs a public parameterless constructor to be owned"));
            return;
        }

        var spec = new OwnedSpec(navigation, owned.Prefix);
        var mapped = 0;
        foreach (var member in PropertiesInDeclarationOrder(ownedType))
        {
            var memberTarget = $"{ownedType.Name}.{member.Name}";
            var column = member.GetCustomAttribute<ColumnAttribute>();
            var ignore = member.GetCustomAttribute<IgnoreAttribute>();
            var enumAsInt = member.GetCustomAttribute<EnumAsIntAttribute>();
            if (member.GetCustomAttribute<RenamedFromAttribute>() is not null)
            {
                errors.Add(new MappingError(
                    "MAP-027", memberTarget, "[RenamedFrom] belongs on an entity's own column, not on an owned type's member"));
            }

            if (member.GetCustomAttributes().Any(a => a is CreatedAtAttribute or UpdatedAtAttribute or SoftDeleteAttribute))
            {
                // Reported, then read as the column it otherwise is, so the owned
                // type does not also fail "maps no columns" (MAP-024): one error.
                errors.Add(new MappingError(
                    "MAP-026", memberTarget, "a hook belongs on an entity's own column, not on an owned type's member"));
            }

            if (member.GetCustomAttributes().Any(a => a is KeyAttribute or GeneratedAttribute or VersionAttribute
                    or ForeignKeyAttribute or OwnedAttribute or ManyToOneAttribute or OneToOneAttribute
                    or OneToManyAttribute or ManyToManyAttribute))
            {
                errors.Add(new MappingError(
                    "MAP-024", memberTarget,
                    "an owned type's members carry only [Column], [EnumAsInt], or [Ignore]: no key, version, generated column, foreign key, relationship, or nested [Owned]"));
                continue;
            }

            if (ignore is not null)
            {
                if (column is not null)
                {
                    errors.Add(new MappingError("MAP-019", memberTarget, "[Ignore] cannot combine with [Column]"));
                }

                continue;
            }

            if (column is null)
            {
                if (enumAsInt is not null)
                {
                    errors.Add(new MappingError(
                        "MAP-019", memberTarget, "mapping attributes require [Column] on the same property"));
                }
                else if (member.GetMethod is { IsPublic: true } && member.SetMethod is { IsPublic: true })
                {
                    errors.Add(new MappingError(
                        "MAP-010", memberTarget,
                        "a public settable property must carry [Column] or [Ignore] (ADR-0004)"));
                }

                continue;
            }

            if (enumAsInt is not null && !member.PropertyType.IsEnum
                && Nullable.GetUnderlyingType(member.PropertyType)?.IsEnum != true)
            {
                errors.Add(new MappingError("MAP-019", memberTarget, "[EnumAsInt] requires an enum property"));
            }

            specs.Add(new MappedPropertySpec(member)
            {
                ExplicitColumn = column.Name,
                EnumAsInt = enumAsInt is not null,
                Owner = spec,
            });
            mapped++;
        }

        if (mapped == 0)
        {
            errors.Add(new MappingError(
                "MAP-024", target, $"'{ownedType.Name}' maps no columns; an owned type needs at least one [Column] member"));
        }
    }

    private static IReadOnlyList<IndexSpec> ReadIndexes(Type entityType, RelationKind kind, List<MappingError> errors)
    {
        var attributes = entityType.GetCustomAttributes<IndexAttribute>(inherit: false).ToArray();
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

    /// <summary>An [Owned] value type (ADR-0030): never an entity, so assembly scans skip it.</summary>
    internal static bool IsOwnedType(Type type) => type.GetCustomAttribute<OwnedAttribute>(inherit: false) is not null;

    internal static bool HasMappingAttributes(Type entityType)
    {
        // The class chain is walked explicitly, each level read without attribute
        // inheritance (ADR-0041) — a base's class-level metadata routes the subclass
        // to this loader, where it is MAP-028 rather than silently adopted.
        for (var type = entityType; type is not null && type != typeof(object); type = type.BaseType)
        {
            if (ClassLevelEntityMetadata(type).Length > 0)
            {
                return true;
            }
        }

        return PropertiesInDeclarationOrder(entityType).Any(p => p.GetCustomAttributes()
            .Any(a => a is ColumnAttribute or IgnoreAttribute or KeyAttribute or GeneratedAttribute
                or VersionAttribute or EnumAsIntAttribute or ForeignKeyAttribute or ManyToOneAttribute
                or OneToOneAttribute or OneToManyAttribute or ManyToManyAttribute or OwnedAttribute));
    }

    /// <summary>
    /// The class-level entity declarations the type itself carries — relation sources
    /// and <c>[Index]</c> — read without attribute inheritance; empty when none.
    /// </summary>
    private static string[] ClassLevelEntityMetadata(Type type)
        => type.GetCustomAttributes(inherit: false)
            .Select(a => a switch
            {
                TableAttribute => "[Table]",
                ViewAttribute => "[View]",
                MaterializedViewAttribute => "[MaterializedView]",
                StatementAttribute => "[Statement]",
                ProcedureAttribute => "[Procedure]",
                IndexAttribute => "[Index]",
                TriggerAttribute { Trigger: not null } => "[Trigger]",
                _ => null,
            })
            .Where(name => name is not null)
            .Distinct()
            .ToArray()!;

    /// <summary>
    /// ADR-0041: a base class shares members only. One carrying a relation source (or an
    /// <c>[Index]</c>, which belongs to a relation) would be an entity, so it may not be
    /// extended by one — <c>MAP-028</c> naming both classes, checked on the chain the
    /// loader already walks for inherited properties.
    /// </summary>
    internal static void CheckBaseClasses(Type entityType, List<MappingError> errors)
    {
        // One error per ancestor, listing its declarations — every loader (attribute,
        // convention, manual builder) calls this: "a mapped type" is mapped by any of them.
        for (var type = entityType.BaseType; type is not null && type != typeof(object); type = type.BaseType)
        {
            if (ClassLevelEntityMetadata(type) is { Length: > 0 } declarations)
            {
                errors.Add(new MappingError(
                    "MAP-028", entityType.Name,
                    $"extends '{type.Name}', which carries {string.Join(" and ", declarations)}: a base class shares members only and is never an entity (ADR-0041); "
                    + "declare the relation source on the concrete class and keep the base a plain member holder"));
            }
        }
    }

    /// <summary>
    /// The scan rule of ADR-0041 (<c>MAP-028</c>): a concrete, sourceless, mapped class
    /// that another scanned class extends. A lone load cannot see it — the class maps
    /// legitimately to a convention-named table — so startup validation and the CLI check
    /// the scanned set as a whole. One error per base class, naming its subclasses.
    /// </summary>
    internal static IReadOnlyList<MappingError> BaseClassesUsedAsEntities(IEnumerable<Type> types)
    {
        var scanned = types.Where(t => t.IsClass && HasMappingAttributes(t) && !IsOwnedType(t) && !IsTriggerType(t)).ToArray();
        var entities = new HashSet<Type>(scanned.Where(t => !t.IsAbstract));
        var extendedBy = new Dictionary<Type, List<string>>();
        foreach (var type in scanned)
        {
            for (var baseType = type.BaseType; baseType is not null && baseType != typeof(object); baseType = baseType.BaseType)
            {
                if (entities.Contains(baseType))
                {
                    if (!extendedBy.TryGetValue(baseType, out var names))
                    {
                        extendedBy[baseType] = names = [];
                    }

                    names.Add(type.Name);
                }
            }
        }

        return extendedBy
            .OrderBy(pair => pair.Key.Name, StringComparer.Ordinal)
            .Select(pair => new MappingError(
                "MAP-028", pair.Key.Name,
                $"is a concrete class without a relation source that '{string.Join("', '", pair.Value.OrderBy(n => n, StringComparer.Ordinal))}' extends: "
                + "a base class shares members only and is never an entity (ADR-0041); declare it abstract"))
            .ToArray();
    }

    /// <summary>
    /// The scanned entity types — concrete, mapped, not owned — after the ADR-0041 scan
    /// rule: when a base class is among them the whole scan refuses with one
    /// <see cref="MappingException"/> carrying every <c>MAP-028</c> (the CLI's entity
    /// commands and the shadow rebuild; SchemaGuard reports instead of throwing).
    /// </summary>
    internal static IReadOnlyList<Type> ScannedEntityTypes(IEnumerable<Type> types)
    {
        var all = types.ToArray();
        var errors = BaseClassesUsedAsEntities(all);
        if (errors.Count > 0)
        {
            throw new MappingException(all.First(t => t.Name == errors[0].Target), errors);
        }

        return all.Where(t => t is { IsClass: true, IsAbstract: false } && HasMappingAttributes(t) && !IsOwnedType(t) && !IsTriggerType(t)).ToArray();
    }

    /// <summary>
    /// Resolves a [OneToOne] navigation (ADR-0019 add.1): a single entity
    /// reference — a collection here is MAP-020 — whose foreign key lives on the
    /// target, named by property (MAP-021 when absent), exactly like [OneToMany]
    /// but singular.
    /// </summary>
    private static void ReadOneToOneNavigation(
        PropertyInfo property,
        string target,
        OneToOneAttribute oneToOne,
        List<RelationshipSpec> relationships,
        List<MappingError> errors)
    {
        var targetType = property.PropertyType;
        if (CollectionElementType(targetType) is not null || !targetType.IsClass || targetType == typeof(string))
        {
            errors.Add(new MappingError(
                "MAP-020", target,
                "a [OneToOne] navigation must be a single entity reference, not a collection"));
            return;
        }

        if (ValidateTargetForeignKeys(
            target, "[OneToOne]", targetType, oneToOne.TargetForeignKeyProperties, errors))
        {
            relationships.Add(new RelationshipSpec(
                property.Name, RelationshipKind.OneToOne, targetType, oneToOne.TargetForeignKeyProperties));
        }
    }

    /// <summary>Every named FK property must exist on the target, and at least one must be named (MAP-021).</summary>
    private static bool ValidateTargetForeignKeys(
        string target, string attribute, Type targetType, IReadOnlyList<string> names, List<MappingError> errors)
    {
        if (names.Count == 0)
        {
            errors.Add(new MappingError(
                "MAP-021", target, $"{attribute} needs at least one target foreign-key property"));
            return false;
        }

        var missing = names.Where(n => targetType.GetProperty(n) is null).ToArray();
        if (missing.Length > 0)
        {
            errors.Add(new MappingError(
                "MAP-021", target,
                $"{attribute} names foreign-key propert{(missing.Length == 1 ? "y" : "ies")} "
                + $"'{string.Join("', '", missing)}' not found on '{targetType.Name}'"));
            return false;
        }

        return true;
    }

    /// <summary>
    /// Attaches a declared cascade (Level 3 M2) to the navigation's spec: declared,
    /// never inferred; a many-to-many navigation cannot cascade (its link rows are
    /// entities the unit of work persists explicitly) — MAP-025.
    /// </summary>
    private static void ApplyCascade(
        CascadeAttribute? cascade, string propertyName, string target, bool manyToMany,
        List<RelationshipSpec> relationships, List<MappingError> errors)
    {
        if (cascade is null)
        {
            return;
        }

        if (manyToMany)
        {
            errors.Add(new MappingError(
                "MAP-025", target,
                "[Cascade] cannot apply to a [ManyToMany] navigation: the link rows are entities the unit of work persists explicitly"));
            return;
        }

        if (cascade.Operations == CascadeOperations.None)
        {
            errors.Add(new MappingError("MAP-025", target, "[Cascade] declares no operation; name Persist, Remove, or All"));
            return;
        }

        var spec = relationships.LastOrDefault(r => r.PropertyName == propertyName);
        if (spec is not null)
        {
            spec.Cascade = cascade.Operations;   // a navigation that failed to resolve already reported its own errors
        }
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
            if (ValidateTargetForeignKeys(
                target, "[OneToMany]", elementType, oneToMany.TargetForeignKeyProperties, errors))
            {
                relationships.Add(new RelationshipSpec(
                    property.Name, RelationshipKind.OneToMany, elementType, oneToMany.TargetForeignKeyProperties));
            }

            return;
        }

        var link = manyToMany!.Through;
        var toOwner = LinkForeignKeys(link, entityType, target, "this type", errors);
        var toTarget = LinkForeignKeys(link, elementType, target, $"'{elementType.Name}'", errors);
        if (toOwner.Count == 0 || toTarget.Count == 0)
        {
            return;
        }

        relationships.Add(new RelationshipSpec(
            property.Name, RelationshipKind.ManyToMany, elementType, foreignKeyProperties: [])
        {
            LinkType = link,
            LinkForeignKeysToOwner = toOwner,
            LinkForeignKeysToTarget = toTarget,
        });
    }

    /// <summary>
    /// The link properties carrying [ForeignKey(referenced)], in declaration order —
    /// a composite-key side legitimately has several, pairing with the referenced
    /// key parts in that order (ADR-0019 add.1); none at all is MAP-022. The
    /// count-vs-key-arity check happens at assembly, where key shapes are known.
    /// </summary>
    private static IReadOnlyList<string> LinkForeignKeys(
        Type link, Type referenced, string target, string side, List<MappingError> errors)
    {
        var candidates = link
            .GetProperties(BindingFlags.Public | BindingFlags.Instance)
            .Where(p => p.GetIndexParameters().Length == 0
                && p.GetCustomAttribute<ForeignKeyAttribute>()?.References == referenced)
            .OrderBy(p => p.MetadataToken)
            .Select(p => p.Name)
            .ToArray();
        if (candidates.Length == 0)
        {
            errors.Add(new MappingError(
                "MAP-022", target,
                $"[ManyToMany] link '{link.Name}' has no [ForeignKey] property referencing {side}"));
        }

        return candidates;
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
