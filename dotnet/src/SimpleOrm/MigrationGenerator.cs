using System.Text;

namespace SimpleOrm;

/// <summary>
/// The diff generator core (ADR-0017): compares entity metadata (the final truth)
/// against the table's latest committed snapshot and describes the change; the
/// emitters turn it into migration source with a **generated Down** (derived from
/// the snapshot — the owner's "downs come from versioned schemas"). Renames are
/// never inferred: they arrive as explicit declarations. Type or nullability
/// changes are not auto-expressible (<c>DDL-004</c>) — those migrations are written
/// by hand.
/// </summary>
public static class MigrationGenerator
{
    public sealed class ColumnSpec(string name, string storageType, bool nullable)
    {
        public string Name { get; } = name;

        public string StorageType { get; } = storageType;

        public bool Nullable { get; } = nullable;
    }

    public sealed class TableDiff
    {
        public bool IsNew { get; init; }

        public List<ColumnSpec> Added { get; } = [];

        public List<ColumnSpec> Removed { get; } = [];

        public List<(string From, string To)> Renamed { get; } = [];

        public List<string> AddedIndexSql { get; } = [];

        public List<string> RemovedIndexNames { get; } = [];

        public List<string> Unsupported { get; } = [];

        public bool HasChanges => IsNew || Added.Count > 0 || Removed.Count > 0 || Renamed.Count > 0
            || AddedIndexSql.Count > 0 || RemovedIndexNames.Count > 0;
    }

    /// <summary>Diffs metadata against the latest snapshot (null = new table). Renames: old column → new column.</summary>
    public static TableDiff Diff(
        EntityMap map, IDialect dialect, TableSchema? snapshot, IReadOnlyDictionary<string, string> renames)
    {
        if (snapshot is null)
        {
            return new TableDiff { IsNew = true };
        }

        var diff = new TableDiff();
        var old = snapshot.Columns.ToDictionary(
            c => c.Name,
            c => new ColumnSpec(c.Name, c.StorageType, c.Nullable),
            StringComparer.OrdinalIgnoreCase);
        var current = map.Properties.ToDictionary(
            p => p.ColumnName,
            p => new ColumnSpec(p.ColumnName, dialect.StorageType(p), p.IsNullable),
            StringComparer.OrdinalIgnoreCase);

        foreach (var rename in renames)
        {
            var from = rename.Key;
            var to = rename.Value;
            if (!old.ContainsKey(from))
            {
                diff.Unsupported.Add($"rename {from} -> {to}: '{from}' is not in the snapshot");
                continue;
            }

            if (!current.ContainsKey(to))
            {
                diff.Unsupported.Add($"rename {from} -> {to}: '{to}' is not a mapped column");
                continue;
            }

            diff.Renamed.Add((from, to));
            var renamedOld = old[from];
            old.Remove(from);
            old[to] = new ColumnSpec(to, renamedOld.StorageType, renamedOld.Nullable);
        }

        foreach (var column in current.Values.Where(c => !old.ContainsKey(c.Name)))
        {
            if (!column.Nullable)
            {
                diff.Unsupported.Add(
                    $"add {column.Name}: non-nullable additions need a default/backfill — write the migration by hand");
            }
            else
            {
                diff.Added.Add(column);
            }
        }

        foreach (var column in old.Values.Where(c => !current.ContainsKey(c.Name)))
        {
            diff.Removed.Add(column);
        }

        foreach (var name in current.Keys.Where(old.ContainsKey))
        {
            var before = old[name];
            var after = current[name];
            if (!string.Equals(before.StorageType, after.StorageType, StringComparison.OrdinalIgnoreCase)
                || before.Nullable != after.Nullable)
            {
                diff.Unsupported.Add(
                    $"column {name}: {before.StorageType}{(before.Nullable ? string.Empty : " not null")} -> "
                    + $"{after.StorageType}{(after.Nullable ? string.Empty : " not null")} — write the migration by hand");
            }
        }

        var oldIndexes = new HashSet<string>(snapshot.Indexes.Select(i => i.Name), StringComparer.OrdinalIgnoreCase);
        foreach (var index in map.Indexes.Where(i => !oldIndexes.Contains(i.Name)))
        {
            diff.AddedIndexSql.Add(dialect.CreateIndexSql(map)
                .First(sql => sql.Contains(" " + index.Name + " ")));
        }

        foreach (var name in oldIndexes.Where(n => map.Indexes.All(i => !string.Equals(i.Name, n, StringComparison.OrdinalIgnoreCase))))
        {
            diff.RemovedIndexNames.Add(name);
        }

        return diff;
    }

    /// <summary>Emits the per-object migration step, Down included (derived from the snapshot).</summary>
    public static string EmitTableStep(
        string rootNamespace, Type entityType, EntityMap map, IDialect dialect,
        long version, string description, TableDiff diff)
    {
        var builder = new StringBuilder();
        builder.Append("namespace ").Append(rootNamespace).Append(".Table.").Append(entityType.Name).AppendLine(";");
        builder.AppendLine();
        builder.AppendLine("/// <summary>Generated by simpleorm diff (ADR-0017); the Down derives from the previous snapshot.</summary>");
        builder.Append("public sealed class V").Append(version.ToString("0000")).Append('_').Append(description)
            .Append(" : TableMigration<global::").Append(entityType.FullName).AppendLine(">");
        builder.AppendLine("{");
        builder.AppendLine("    public override void Action(TableActions actions)");
        builder.AppendLine("    {");
        if (diff.IsNew)
        {
            AppendSqlAction(builder, dialect.CreateTableSql(map));
            foreach (var indexSql in dialect.CreateIndexSql(map))
            {
                AppendSqlAction(builder, indexSql);
            }
        }
        else
        {
            foreach (var (from, to) in diff.Renamed)
            {
                builder.Append("        actions.RenameColumn(\"").Append(from).Append("\", \"").Append(to).AppendLine("\");");
            }

            foreach (var column in diff.Added)
            {
                builder.Append("        actions.AddColumn(\"").Append(column.Name)
                    .Append("\", \"").Append(column.StorageType).AppendLine("\");");
            }

            foreach (var column in diff.Removed)
            {
                builder.Append("        actions.RemoveColumn(\"").Append(column.Name).AppendLine("\");");
            }

            foreach (var indexSql in diff.AddedIndexSql)
            {
                AppendSqlAction(builder, indexSql);
            }

            foreach (var name in diff.RemovedIndexNames)
            {
                builder.Append("        actions.DropIndex(\"").Append(name).AppendLine("\");");
            }
        }

        builder.AppendLine("    }");
        builder.AppendLine();
        builder.AppendLine("    public override void Down(TableActions actions)");
        builder.AppendLine("    {");
        if (diff.IsNew)
        {
            builder.AppendLine("        actions.DropTable();");
        }
        else
        {
            foreach (var (from, to) in diff.Renamed)
            {
                builder.Append("        actions.RenameColumn(\"").Append(to).Append("\", \"").Append(from).AppendLine("\");");
            }

            foreach (var column in diff.Removed)
            {
                // Structure returns; the data it held does not — add a PreDown/PostDown by hand if it must.
                builder.Append("        actions.AddColumn(\"").Append(column.Name)
                    .Append("\", \"").Append(column.StorageType).Append('"');
                builder.AppendLine(column.Nullable
                    ? ");"
                    : ");   // was NOT NULL — restore the constraint (and a backfill) by hand");
            }

            foreach (var column in diff.Added)
            {
                builder.Append("        actions.RemoveColumn(\"").Append(column.Name).AppendLine("\");");
            }

            foreach (var name in diff.RemovedIndexNames)
            {
                builder.Append("        // dropped index '").Append(name).AppendLine("' is not restored automatically; recreate by hand if needed");
            }

            foreach (var indexSql in diff.AddedIndexSql)
            {
                var indexName = indexSql.Split(' ').SkipWhile(t => t != "exists").Skip(1).First();
                builder.Append("        actions.DropIndex(\"").Append(indexName).AppendLine("\");");
            }
        }

        builder.AppendLine("    }");
        builder.AppendLine("}");
        return builder.ToString();
    }

    /// <summary>
    /// Emits a view/materialized-view step from **literal DDL** (never
    /// metadata-rendered, so the applied step's checksum can never drift when the
    /// definition changes again). With no <paramref name="previousDdl"/> this is
    /// the object's create (Down drops it); otherwise a change (drop + new
    /// definition, Down restoring the previous definition from its snapshot).
    /// </summary>
    public static string EmitViewStep(
        string rootNamespace, Type entityType, string kindFolder, string objectName,
        long version, string description, string createDdl, string? previousDdl)
    {
        var dropSql = (kindFolder == "MaterializedView" ? "drop materialized view if exists " : "drop view if exists ")
            + objectName;
        var builder = new StringBuilder();
        builder.Append("namespace ").Append(rootNamespace).Append('.').Append(kindFolder).Append('.')
            .Append(entityType.Name).AppendLine(";");
        builder.AppendLine();
        builder.AppendLine("/// <summary>Generated by simpleorm diff (ADR-0017): literal DDL, guarded by the expected previous definition (MIG-012 on outside drift); the Down restores the previous snapshot's definition.</summary>");
        builder.Append("public sealed class V").Append(version.ToString("0000")).Append('_').Append(description)
            .Append(" : ViewMigration<global::").Append(entityType.FullName).AppendLine(">");
        builder.AppendLine("{");
        builder.AppendLine("    public override void Action(ViewActions actions)");
        builder.AppendLine("    {");
        if (previousDdl is not null)
        {
            AppendViewAction(builder, "ExpectDefinition", previousDdl);
            AppendViewAction(builder, "Sql", dropSql);
        }

        AppendViewAction(builder, "Sql", createDdl);
        builder.AppendLine("    }");
        builder.AppendLine();
        builder.AppendLine("    public override void Down(ViewActions actions)");
        builder.AppendLine("    {");
        AppendViewAction(builder, "ExpectDefinition", createDdl);
        AppendViewAction(builder, "Sql", dropSql);
        if (previousDdl is not null)
        {
            AppendViewAction(builder, "Sql", previousDdl);
        }

        builder.AppendLine("    }");
        builder.AppendLine("}");
        return builder.ToString();
    }

    private static void AppendViewAction(StringBuilder builder, string method, string sql)
        => builder.Append("        actions.").Append(method).Append("(\"").Append(EscapeLiteral(sql)).AppendLine("\");");

    private static string EscapeLiteral(string sql)
        => sql.Replace("\\", "\\\\").Replace("\"", "\\\"");

    /// <summary>Emits the root version composing the steps in the given order.</summary>
    public static string EmitRoot(string rootNamespace, long version, IReadOnlyList<string> stepTypeReferences)
    {
        var builder = new StringBuilder();
        builder.Append("namespace ").Append(rootNamespace).AppendLine(";");
        builder.AppendLine();
        builder.AppendLine("/// <summary>Generated by simpleorm diff (ADR-0017); review the order before committing.</summary>");
        builder.Append("public sealed class V").Append(version.ToString("0000")).AppendLine(" : MigrationVersion");
        builder.AppendLine("{");
        builder.AppendLine("    public override void Compose(VersionBuilder version) => version");
        for (var i = 0; i < stepTypeReferences.Count; i++)
        {
            builder.Append("        .Apply<").Append(stepTypeReferences[i]).Append(">()")
                .AppendLine(i == stepTypeReferences.Count - 1 ? ";" : string.Empty);
        }

        builder.AppendLine("}");
        return builder.ToString();
    }

    private static void AppendSqlAction(StringBuilder builder, string sql)
    {
        if (!sql.Contains('\n'))
        {
            builder.Append("        actions.Sql(\"").Append(EscapeLiteral(sql)).AppendLine("\");");
            return;
        }

        builder.AppendLine("        actions.Sql(");
        builder.AppendLine("            \"\"\"");
        foreach (var line in sql.Replace("\r", string.Empty).Split('\n'))
        {
            builder.Append("            ").AppendLine(line);
        }

        builder.AppendLine("            \"\"\");");
    }
}
