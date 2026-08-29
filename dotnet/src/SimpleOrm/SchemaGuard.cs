using System.Data.Common;
using System.Reflection;
using System.Text.RegularExpressions;

namespace SimpleOrm;

/// <summary>
/// The rules (§7.18–21): validates every registered query/command and every mapped
/// entity against the real database **without executing anything that writes** —
/// statements are prepared and described inside a transaction that is always rolled
/// back. Every violation is collected; one <see cref="SchemaValidationException"/>
/// carries the complete report. Runs at startup and in a test that calls the same
/// code; there is no warn-only mode.
/// </summary>
public static class SchemaGuard
{
    private static readonly Regex SelectStar = new(
        @"(?i)(?<=select|,)\s*([A-Za-z_]\w*\s*\.\s*)?\*", RegexOptions.Compiled);

    private static readonly Regex NotNullComment = new(
        @"(?i)--\s*notnull:\s*([^\r\n]+)", RegexOptions.Compiled);

    /// <summary>Validates the assembly's registries, entities, and migration state against the session's database.</summary>
    public static Task ValidateAsync(Db db, Assembly assembly, CancellationToken ct)
        => ValidateAsync(db, assembly, migrationsNamespace: null, ct);

    public static async Task ValidateAsync(Db db, Assembly assembly, string? migrationsNamespace, CancellationToken ct)
    {
        var errors = new List<ValidationError>();
        await CheckMigrationsAsync(db, assembly, migrationsNamespace, errors, ct).ConfigureAwait(false);
        await ValidateTypesCoreAsync(db, assembly.GetExportedTypes(), errors, ct).ConfigureAwait(false);
        Throw(errors);
    }

    /// <summary>Validates an explicit set of registry classes and entity types (test harnesses, partial checks).</summary>
    public static async Task ValidateTypesAsync(Db db, IEnumerable<Type> types, CancellationToken ct)
    {
        var errors = new List<ValidationError>();
        await ValidateTypesCoreAsync(db, types.ToArray(), errors, ct).ConfigureAwait(false);
        Throw(errors);
    }

    private static void Throw(List<ValidationError> errors)
    {
        if (errors.Count > 0)
        {
            throw new SchemaValidationException(errors);
        }
    }

    private static async Task ValidateTypesCoreAsync(
        Db db, IReadOnlyList<Type> types, List<ValidationError> errors, CancellationToken ct)
    {
        // The rollback shield: nothing validated here can leave a trace.
        using var shield = db.Connection.BeginTransaction();
        var cache = new Dictionary<string, Dictionary<string, ColumnInfo>>(StringComparer.OrdinalIgnoreCase);
        try
        {
            foreach (var (source, sql, argsType, resultType) in DiscoverRegistry(types))
            {
                await ValidateStatementAsync(db, shield, cache, source, sql, argsType, resultType, errors, ct).ConfigureAwait(false);
            }

            foreach (var entity in types.Where(t =>
                t is { IsClass: true, IsAbstract: false } && EntityMapLoader.HasMappingAttributes(t)))
            {
                await ValidateEntityAsync(db, shield, cache, entity, errors, ct).ConfigureAwait(false);
            }
        }
        finally
        {
            shield.Rollback();
        }
    }

    // --- registry ------------------------------------------------------------------

    private static IEnumerable<(string Source, string Sql, Type? ArgsType, Type? ResultType)> DiscoverRegistry(
        IReadOnlyList<Type> types)
    {
        foreach (var type in types)
        {
            foreach (var field in type.GetFields(BindingFlags.Public | BindingFlags.Static))
            {
                if (!field.FieldType.IsGenericType)
                {
                    continue;
                }

                var definition = field.FieldType.GetGenericTypeDefinition();
                var source = $"{type.Name}.{field.Name}";
                var value = field.GetValue(null);
                if (value is null)
                {
                    continue;
                }

                var arguments = field.FieldType.GetGenericArguments();
                if (definition == typeof(Query<,>))
                {
                    yield return (source, SqlOf(value), arguments[0], arguments[1]);
                }
                else if (definition == typeof(Command<>))
                {
                    yield return (source, SqlOf(value), arguments[0], null);
                }
            }
        }

        static string SqlOf(object entry)
            => ((SqlSource)entry.GetType().GetProperty("Source")!.GetValue(entry)!).Sql;
    }

    private static async Task ValidateStatementAsync(
        Db db, DbTransaction shield, Dictionary<string, Dictionary<string, ColumnInfo>> cache, string source, string sql,
        Type? argsType, Type? resultType, List<ValidationError> errors, CancellationToken ct)
    {
        var placeholders = SqlPlaceholders.Find(sql);

        // PRM-001/PRM-002 statically, both directions (§7.13).
        if (argsType is not null)
        {
            var properties = argsType.GetProperties(BindingFlags.Public | BindingFlags.Instance)
                .Where(p => p.GetMethod is { IsPublic: true })
                .Select(p => p.Name)
                .ToArray();
            foreach (var placeholder in placeholders.Where(p =>
                !properties.Any(n => string.Equals(n, p, StringComparison.OrdinalIgnoreCase))))
            {
                errors.Add(new ValidationError(
                    "PRM-001", source, $"SQL parameter @{placeholder} has no property on {argsType.Name}"));
            }

            foreach (var property in properties.Where(n =>
                !placeholders.Any(p => string.Equals(n, p, StringComparison.OrdinalIgnoreCase))))
            {
                errors.Add(new ValidationError(
                    "PRM-002", source, $"property {argsType.Name}.{property} is never used by the SQL"));
            }
        }

        // Lints on the raw text.
        var stripped = StripLiteralsAndComments(sql);
        if (SelectStar.IsMatch(stripped))
        {
            errors.Add(new ValidationError("VAL-021", source, "SELECT * is not allowed; list columns explicitly"));
        }

        if (Regex.IsMatch(stripped, @"(?i)current_timestamp|datetime\s*\(\s*'now'"))
        {
            errors.Add(new ValidationError(
                "VAL-020", source, "current_timestamp/datetime('now') store datetimes without a UTC marker; bind an ISO-8601 Z value instead"));
        }

        // Prepare / describe without executing anything that persists.
        using var command = db.Connection.CreateCommand();
        command.Transaction = shield;
        command.CommandText = sql;
        foreach (var placeholder in placeholders)
        {
            var parameter = command.CreateParameter();
            parameter.ParameterName = "@" + placeholder;
            parameter.Value = DBNull.Value;
            command.Parameters.Add(parameter);
        }

        if (resultType is null)
        {
            try
            {
                command.Prepare();
            }
            catch (DbException exception)
            {
                errors.Add(new ValidationError("VAL-001", source, exception.Message));
            }

            return;
        }

        DbDataReader reader;
        try
        {
            reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
        }
        catch (DbException exception)
        {
            errors.Add(new ValidationError("VAL-001", source, exception.Message));
            return;
        }

        try
        {
            // Result shape (MAP-001/002/003) through the one mapping pipeline.
            try
            {
                typeof(ResultMapper).GetMethod(nameof(ResultMapper.CreatePlan))!
                    .MakeGenericMethod(resultType)
                    .Invoke(db.Mapper, [reader, source]);
            }
            catch (TargetInvocationException exception) when (exception.InnerException is SimpleOrmException mapping)
            {
                errors.Add(new ValidationError(mapping.Code, source, mapping.Message));
            }

            await ValidateResultColumnsAsync(db, shield, cache, source, sql, reader, resultType, errors, ct).ConfigureAwait(false);
        }
        finally
        {
            reader.Dispose();
        }
    }

    private static async Task ValidateResultColumnsAsync(
        Db db, DbTransaction shield, Dictionary<string, Dictionary<string, ColumnInfo>> cache, string source, string sql,
        DbDataReader reader, Type resultType, List<ValidationError> errors, CancellationToken ct)
    {
        var notNullOverrides = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (Match match in NotNullComment.Matches(sql))
        {
            foreach (var name in match.Groups[1].Value.Split(','))
            {
                notNullOverrides.Add(name.Trim());
            }
        }

        var schema = TryGetSchemaTable(reader);
        var entityMap = EntityMapLoader.HasMappingAttributes(resultType) ? db.Maps.Load(resultType) : null;

        for (var i = 0; i < reader.FieldCount; i++)
        {
            var column = reader.GetName(i);
            var member = ResolveMember(resultType, entityMap, column);
            if (member is null)
            {
                continue;   // MAP-001 already reported by the pipeline
            }

            var (memberType, memberNullable) = member.Value;
            var origin = OriginOf(schema, i);
            if (origin is { } o)
            {
                var info = await GetColumnInfoAsync(db, shield, cache, o.Table, o.Column, ct).ConfigureAwait(false);
                if (info is { } columnInfo)
                {
                    var enumAsInt = entityMap?.Properties
                        .FirstOrDefault(p => string.Equals(p.ColumnName, column, StringComparison.OrdinalIgnoreCase))
                        ?.EnumAsInt ?? false;
                    if (!db.Options.Dialect.IsDeclaredTypeCompatible(columnInfo.DeclaredType, memberType, enumAsInt)
                        && !db.Converter.HasHandler(Nullable.GetUnderlyingType(memberType) ?? memberType))
                    {
                        errors.Add(new ValidationError(
                            "VAL-011", source,
                            $"column '{column}' is declared {columnInfo.DeclaredType} in {o.Table}, incompatible with {memberType.Name} (no handler)"));
                    }

                    if (!columnInfo.NotNull && !memberNullable)
                    {
                        errors.Add(new ValidationError(
                            "VAL-010", source, $"nullable column '{column}' maps to non-nullable {memberType.Name}"));
                    }

                    continue;
                }
            }

            // Expression column: nullability unknowable — require nullable or the comment.
            if (!memberNullable && !notNullOverrides.Contains(column))
            {
                errors.Add(new ValidationError(
                    "VAL-010", source,
                    $"expression column '{column}' has unknown nullability; make the member nullable or add '-- notnull: {column}'"));
            }
        }
    }

    // --- entities ------------------------------------------------------------------

    private static async Task ValidateEntityAsync(
        Db db, DbTransaction shield, Dictionary<string, Dictionary<string, ColumnInfo>> cache, Type entityType,
        List<ValidationError> errors, CancellationToken ct)
    {
        EntityMap map;
        try
        {
            map = db.Maps.Load(entityType);
        }
        catch (MappingException exception)
        {
            errors.AddRange(exception.Errors.Select(e => new ValidationError(e.Code, entityType.Name, e.Message)));
            return;
        }

        switch (map.Kind)
        {
            case RelationKind.Procedure when !db.Options.Dialect.SupportsProcedures:
            case RelationKind.MaterializedView when !db.Options.Dialect.SupportsMaterializedViews:
                return;   // dormant on this dialect (capability-gated)
            case RelationKind.Statement:
                await ValidateStatementAsync(
                    db, shield, cache, entityType.Name + " [Statement]", map.DefiningSql!,
                    argsType: null, resultType: entityType, errors, ct).ConfigureAwait(false);
                return;
        }

        var columns = await GetRelationColumnsAsync(db, shield, map.RelationName!, ct).ConfigureAwait(false);
        if (columns.Count == 0)
        {
            errors.Add(new ValidationError(
                "VAL-012", entityType.Name, $"relation '{map.RelationName}' does not exist in the database"));
            return;
        }

        foreach (var property in map.Properties)
        {
            if (!columns.TryGetValue(property.ColumnName, out var info))
            {
                errors.Add(new ValidationError(
                    "VAL-013", entityType.Name, $"mapped column '{property.ColumnName}' does not exist in '{map.RelationName}'"));
                continue;
            }

            if (map.Kind != RelationKind.Table)
            {
                continue;   // views report neither declared types nor nullability reliably (§7.19)
            }

            if (!db.Options.Dialect.IsDeclaredTypeCompatible(info.DeclaredType, property.ClrType, property.EnumAsInt)
                && !db.Converter.HasHandler(Nullable.GetUnderlyingType(property.ClrType) ?? property.ClrType))
            {
                errors.Add(new ValidationError(
                    "VAL-011", entityType.Name,
                    $"column '{property.ColumnName}' is declared {info.DeclaredType}, incompatible with {property.ClrType.Name} (no handler)"));
            }

            if (!info.NotNull && !property.IsNullable)
            {
                errors.Add(new ValidationError(
                    "VAL-010", entityType.Name,
                    $"nullable column '{property.ColumnName}' maps to non-nullable {property.PropertyName}"));
            }
        }
    }

    // --- migrations (MIG-030 and history health) ------------------------------------

    private static async Task CheckMigrationsAsync(
        Db db, Assembly assembly, string? migrationsNamespace, List<ValidationError> errors, CancellationToken ct)
    {
        MigrationRunner runner;
        try
        {
            runner = new MigrationRunner(db, assembly, migrationsNamespace);
        }
        catch (SimpleOrmException exception)
        {
            errors.Add(new ValidationError(exception.Code, "migrations", exception.Message));
            return;
        }

        foreach (var entry in await runner.StatusAsync(ct).ConfigureAwait(false))
        {
            var (code, message) = entry.State switch
            {
                MigrationState.Pending => ("MIG-030", "pending — apply migrations before starting the application"),
                MigrationState.Drifted => ("MIG-010", "applied with a different checksum than the code renders"),
                MigrationState.Unknown => ("MIG-011", "applied in the database but unknown to the code"),
                _ => (null as string, string.Empty),
            };
            if (code is not null)
            {
                errors.Add(new ValidationError(code, $"V{entry.Version:0000} {entry.ObjectName}", message));
            }
        }
    }

    // --- introspection helpers ------------------------------------------------------

    private sealed record ColumnInfo(string DeclaredType, bool NotNull);

    private static async Task<Dictionary<string, ColumnInfo>> GetRelationColumnsAsync(
        Db db, DbTransaction shield, string relation, CancellationToken ct)
    {
        var columns = new Dictionary<string, ColumnInfo>(StringComparer.OrdinalIgnoreCase);
        using var command = db.Connection.CreateCommand();
        command.Transaction = shield;
        command.CommandText = db.Options.Dialect.ColumnsInfoSql;
        var parameter = command.CreateParameter();
        parameter.ParameterName = "@relation";
        parameter.Value = relation;
        command.Parameters.Add(parameter);

        var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
        try
        {
            while (await reader.ReadAsync(ct).ConfigureAwait(false))
            {
                // Primary-key columns are implicitly NOT NULL (SQLite reports notnull=0 for INTEGER PRIMARY KEY).
                var notNull = (long)reader.GetValue(2) != 0 || (long)reader.GetValue(3) != 0;
                columns[reader.GetString(0)] = new ColumnInfo(reader.GetString(1), notNull);
            }
        }
        finally
        {
            reader.Dispose();
        }

        return columns;
    }

    private static async Task<ColumnInfo?> GetColumnInfoAsync(
        Db db, DbTransaction shield, Dictionary<string, Dictionary<string, ColumnInfo>> cache,
        string table, string column, CancellationToken ct)
    {
        if (!cache.TryGetValue(table, out var columns))
        {
            columns = await GetRelationColumnsAsync(db, shield, table, ct).ConfigureAwait(false);
            cache[table] = columns;
        }

        return columns.TryGetValue(column, out var info) ? info : null;
    }

    private static (string Table, string Column)? OriginOf(System.Data.DataTable? schema, int ordinal)
    {
        if (schema is null || ordinal >= schema.Rows.Count)
        {
            return null;
        }

        var row = schema.Rows[ordinal];
        var table = Value(row, "BaseTableName");
        var column = Value(row, "BaseColumnName");
        return table is null || column is null ? null : (table, column);

        static string? Value(System.Data.DataRow row, string name)
            => row.Table.Columns.Contains(name) && row[name] is string s && s.Length > 0 ? s : null;
    }

    private static System.Data.DataTable? TryGetSchemaTable(DbDataReader reader)
    {
        try
        {
            return reader.GetSchemaTable();
        }
        catch (Exception)
        {
            return null;
        }
    }

    private static (Type Type, bool Nullable)? ResolveMember(Type resultType, EntityMap? map, string column)
    {
        if (map is not null)
        {
            var mapped = map.Properties.FirstOrDefault(
                p => string.Equals(p.ColumnName, column, StringComparison.OrdinalIgnoreCase));
            return mapped is null ? null : (mapped.ClrType, mapped.IsNullable);
        }

        if (IsScalarish(resultType))
        {
            return (resultType, !resultType.IsValueType || Nullable.GetUnderlyingType(resultType) is not null);
        }

        var property = resultType.GetProperties(BindingFlags.Public | BindingFlags.Instance)
            .FirstOrDefault(p => NamesMatch(column, p.Name));
        if (property is not null)
        {
            return (property.PropertyType, NullabilityReader.IsNullable(property));
        }

        return null;
    }

    private static bool IsScalarish(Type type)
    {
        var underlying = Nullable.GetUnderlyingType(type) ?? type;
        return underlying.IsPrimitive || underlying.IsEnum || underlying == typeof(string)
            || underlying == typeof(decimal) || underlying == typeof(Guid)
            || underlying == typeof(DateTime) || underlying == typeof(DateTimeOffset) || underlying == typeof(byte[]);
    }

    private static bool NamesMatch(string column, string member)
        => string.Equals(
            column.Replace("_", string.Empty),
            member.Replace("_", string.Empty),
            StringComparison.OrdinalIgnoreCase);

    private static string StripLiteralsAndComments(string sql)
        => Regex.Replace(
            Regex.Replace(sql, @"'([^']|'')*'", "''"),
            @"--[^\r\n]*", string.Empty);
}
