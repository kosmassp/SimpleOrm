namespace SimpleOrm;

/// <summary>
/// Derives a step's rollback DDL from the versioned snapshots at
/// <c>migrate down</c> time (ADR-0018): nobody writes <c>Down()</c> — the previous
/// schema is recorded, so the reverse is deduced. The step's typed renames are the
/// one piece snapshots can't recover (a rename and a drop+add look identical
/// between two shapes), so they invert from the step's own actions,
/// data-preservingly; everything else — restored columns, dropped columns, index
/// changes, a view's previous definition — comes from the snapshot diff.
/// <c>Down()</c> remains the manual override; a change the snapshots can't express
/// (type/nullability) still refuses with <c>MIG-020</c>.
/// </summary>
internal static class DownDeriver
{
    /// <summary>
    /// The derived rollback for (object, version), or null when the snapshots
    /// can't support it (no snapshot at the version). Throws MIG-020 for a
    /// same-name type/nullability change — override Down() for those.
    /// </summary>
    public static IReadOnlyList<MigrationStatement>? Derive(
        string objectName, long version, SnapshotSet snapshots,
        IReadOnlyList<(string From, string To)> upRenames, List<string> notices)
    {
        var at = snapshots.At(objectName, version);
        if (at is null)
        {
            return null;
        }

        var before = snapshots.LatestBefore(objectName, version);
        return at.Ddl is not null
            ? DeriveView(objectName, at.Ddl, before?.Ddl)
            : DeriveTable(objectName, version, at.Table!, before?.Table, upRenames, notices);
    }

    private static IReadOnlyList<MigrationStatement> DeriveView(string objectName, string ddlAt, string? ddlBefore)
    {
        var statements = new List<MigrationStatement>
        {
            // The apply guard in reverse (MIG-012): a definition hotfixed outside
            // the code is not silently destroyed by a rollback either.
            new(ddlAt, "derived expect " + objectName, guardView: objectName),
            new("drop view if exists " + objectName, "derived drop " + objectName),
        };
        if (ddlBefore is not null)
        {
            statements.Add(new MigrationStatement(ddlBefore, "derived restore " + objectName));
        }

        return statements;
    }

    private static IReadOnlyList<MigrationStatement> DeriveTable(
        string objectName, long version, TableSchema at, TableSchema? before,
        IReadOnlyList<(string From, string To)> upRenames, List<string> notices)
    {
        if (before is null)
        {
            return [new MigrationStatement("drop table " + objectName, "derived drop " + objectName)];
        }

        var statements = new List<MigrationStatement>();

        // Renames invert first (rename → add → remove ordering, §7.22) and map the
        // current names back before the shapes are compared.
        var nameAtToBefore = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (var (from, to) in upRenames.Reverse())
        {
            statements.Add(new MigrationStatement(
                $"alter table {objectName} rename column {to} to {from}", "derived rename " + objectName));
            nameAtToBefore[to] = from;
        }

        var current = at.Columns.ToDictionary(
            c => nameAtToBefore.TryGetValue(c.Name, out var renamed) ? renamed : c.Name,
            c => c,
            StringComparer.OrdinalIgnoreCase);
        var previous = before.Columns.ToDictionary(c => c.Name, c => c, StringComparer.OrdinalIgnoreCase);

        foreach (var column in previous.Values.Where(c => !current.ContainsKey(c.Name)))
        {
            var sql = $"alter table {objectName} add column {column.Name} {column.StorageType}";
            if (!column.Nullable)
            {
                // The database refuses a bare NOT NULL addition; the structure
                // returns nullable, and the data a PreDown/PostDown hook restores.
                notices.Add(
                    $"V{version:0000} {objectName}.{column.Name}: restored nullable — the NOT NULL constraint (and the data) are not derivable");
            }

            statements.Add(new MigrationStatement(sql, "derived add " + objectName));
        }

        // Keys carry the renamed-back names — by the time these run, the reverse
        // renames above have already been applied.
        foreach (var name in current.Keys.Where(n => !previous.ContainsKey(n)))
        {
            statements.Add(new MigrationStatement(
                $"alter table {objectName} drop column {name}", "derived remove " + objectName));
        }

        foreach (var name in current.Keys.Where(previous.ContainsKey))
        {
            var now = current[name];
            var then = previous[name];
            if (!string.Equals(now.StorageType, then.StorageType, StringComparison.OrdinalIgnoreCase)
                || now.Nullable != then.Nullable)
            {
                throw new SimpleOrmException(
                    "MIG-020", $"V{version:0000} {objectName}",
                    $"column {name} changed type/nullability at this version; that rollback cannot be derived — override Down()");
            }
        }

        // Indexes match structurally (ADR-0017 add.2) here too.
        var atIndexes = at.Indexes.ToDictionary(MigrationGenerator.IndexSignature, i => i, StringComparer.Ordinal);
        var beforeIndexes = before.Indexes.ToDictionary(MigrationGenerator.IndexSignature, i => i, StringComparer.Ordinal);
        foreach (var pair in atIndexes.Where(p => !beforeIndexes.ContainsKey(ApplyRenames(p.Key, nameAtToBefore))))
        {
            statements.Add(new MigrationStatement("drop index " + pair.Value.Name, "derived drop index"));
        }

        foreach (var pair in beforeIndexes.Where(p =>
            !atIndexes.Keys.Any(sig => ApplyRenames(sig, nameAtToBefore) == p.Key)))
        {
            statements.Add(new MigrationStatement(SnapshotDdl.CreateIndexSql(objectName, pair.Value), "derived add index"));
        }

        return statements;
    }

    /// <summary>Rewrites an index signature's column names through the reverse-rename map.</summary>
    private static string ApplyRenames(string signature, Dictionary<string, string> nameAtToBefore)
    {
        if (nameAtToBefore.Count == 0)
        {
            return signature;
        }

        var split = signature.Split('|');
        var columns = split[1].Split(',')
            .Select(part =>
            {
                var descending = part.EndsWith(" desc", StringComparison.Ordinal);
                var column = descending ? part.Substring(0, part.Length - 5) : part;
                return (nameAtToBefore.TryGetValue(column, out var renamed) ? renamed.ToLowerInvariant() : column)
                    + (descending ? " desc" : string.Empty);
            });
        return split[0] + "|" + string.Join(",", columns);
    }
}
