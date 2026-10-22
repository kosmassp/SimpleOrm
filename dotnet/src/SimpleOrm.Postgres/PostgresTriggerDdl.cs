using System.Data.Common;
using System.Text.RegularExpressions;

namespace SimpleOrm.Postgres;

/// <summary>
/// The inverse of <see cref="PostgresDialect.CreateTriggerSql"/> for the shadow
/// replayer's self-check (ADR-0043): the two rows <c>TriggerDefinitionSql</c>
/// reconstructs — the function (body verbatim from <c>pg_proc.prosrc</c>) and the
/// trigger header (from <c>pg_trigger</c>) — parse back to the neutral
/// declaration, the dialect's expansions folded back into the tokens
/// (<c>(select new."a" as "a", new."b" as "b")</c> → <c>{new: a, b}</c>,
/// <c>now()</c> → <c>{now}</c>) and the rendered <c>return</c> tail removed.
/// A text this grammar does not match (a trigger written by hand) yields null
/// with a note.
/// </summary>
public static class PostgresTriggerDdl
{
    private static readonly Regex Header = new(
        @"^create\s+trigger\s+""(?<name>[^""]+)""\s+(?<timing>before|after|instead\s+of)\s+(?<events>.+?)\s+on\s+""(?<relation>[^""]+)""\s+for\s+each\s+row\s+execute\s+function\s+""(?<fn>[^""]+)""\(\)\s*$",
        RegexOptions.Compiled | RegexOptions.IgnoreCase);

    private static readonly Regex Function = new(
        @"^create\s+or\s+replace\s+function\s+""(?<fn>[^""]+)""\(\)\s+returns\s+trigger\s+language\s+plpgsql\s+as\s+\$\$(?<src>[\s\S]*)\$\$\s*$",
        RegexOptions.Compiled | RegexOptions.IgnoreCase);

    private static readonly Regex Body = new(
        @"^\s*begin\s+(?<body>[\s\S]*?)\s*(?<tail>return\s+null;|if\s+tg_op\s*=\s*'DELETE'\s+then\s+return\s+old;\s+end\s+if;\s+return\s+new;)\s*end\s*$",
        RegexOptions.Compiled | RegexOptions.IgnoreCase);

    private static readonly Regex RowSelect = new(
        @"\(select\s+(?<row>new|old)\.""(?<first>[^""]+)""\s+as\s+""[^""]+""(?<rest>(\s*,\s*(new|old)\.""[^""]+""\s+as\s+""[^""]+"")*)\)",
        RegexOptions.Compiled | RegexOptions.IgnoreCase);

    private static readonly Regex ColumnPair = new(
        @"(?<row>new|old)\.""(?<col>[^""]+)""\s+as\s+""(?<alias>[^""]+)""", RegexOptions.Compiled | RegexOptions.IgnoreCase);

    private static readonly Regex UpdateOf = new(@"^update(\s+of\s+(?<cols>.+))?$", RegexOptions.Compiled | RegexOptions.IgnoreCase);

    public static TriggerSnapshot? Read(DbConnection connection, string triggerName, out string? note)
    {
        var rows = new List<string>();
        using (var command = connection.CreateCommand())
        {
            command.CommandText = new PostgresDialect().TriggerDefinitionSql;
            var parameter = command.CreateParameter();
            parameter.ParameterName = "@relation";
            parameter.Value = triggerName;
            command.Parameters.Add(parameter);
            using var reader = command.ExecuteReader();
            while (reader.Read())
            {
                if (!reader.IsDBNull(0))
                {
                    rows.Add(reader.GetString(0));
                }
            }
        }

        note = null;
        if (rows.Count == 0)
        {
            return null;   // absent: dropped at this version
        }

        var header = rows.Select(r => Header.Match(r.Trim())).FirstOrDefault(m => m.Success);
        var function = rows.Select(r => Function.Match(r.Trim())).FirstOrDefault(m => m.Success);
        if (header is null || function is null)
        {
            note = $"trigger {triggerName}: the stored definition does not match the dialect's grammar (hand-written?); skipped";
            return null;
        }

        var body = Body.Match(function.Groups["src"].Value);
        if (!body.Success)
        {
            note = $"trigger {triggerName}: the function body does not match the dialect's wrapper (hand-written?); skipped";
            return null;
        }

        var timing = header.Groups["timing"].Value.ToLowerInvariant() switch
        {
            "before" => TriggerTiming.Before,
            "after" => TriggerTiming.After,
            _ => TriggerTiming.InsteadOf,
        };

        var events = TriggerEvent.None;
        IReadOnlyList<string> updateOf = [];
        foreach (var part in Regex.Split(header.Groups["events"].Value, @"\s+or\s+", RegexOptions.IgnoreCase).Select(p => p.Trim()))
        {
            if (string.Equals(part, "insert", StringComparison.OrdinalIgnoreCase))
            {
                events |= TriggerEvent.Insert;
            }
            else if (string.Equals(part, "delete", StringComparison.OrdinalIgnoreCase))
            {
                events |= TriggerEvent.Delete;
            }
            else if (UpdateOf.Match(part) is { Success: true } update)
            {
                events |= TriggerEvent.Update;
                if (update.Groups["cols"].Success)
                {
                    updateOf = update.Groups["cols"].Value.Split(',').Select(c => c.Trim().Trim('"')).ToArray();
                }
            }
            else
            {
                note = $"trigger {triggerName}: unknown event '{part}'; skipped";
                return null;
            }
        }

        var text = RowSelect.Replace(body.Groups["body"].Value, FoldRowSelect);
        text = Regex.Replace(text, @"\bnow\(\)", "{now}", RegexOptions.IgnoreCase);
        var relation = header.Groups["relation"].Value;
        return new TriggerSnapshot(
            relation, IsView(connection, relation),
            new TriggerDefinition(triggerName, timing, events, text.Trim(), updateOf));
    }

    /// <summary><c>(select new."a" as "a", new."b" as "b")</c> → <c>{new: a, b}</c> — only the exact expansion; anything else is the author's SQL.</summary>
    private static string FoldRowSelect(Match m)
    {
        var row = m.Groups["row"].Value.ToLowerInvariant();
        var columns = new List<string>();
        foreach (Match part in ColumnPair.Matches(m.Value))
        {
            if (!string.Equals(part.Groups["row"].Value, row, StringComparison.OrdinalIgnoreCase)
                || part.Groups["col"].Value != part.Groups["alias"].Value)
            {
                return m.Value;
            }

            columns.Add(part.Groups["col"].Value);
        }

        return "{" + row + ": " + string.Join(", ", columns) + "}";
    }

    private static bool IsView(DbConnection connection, string relation)
    {
        using var command = connection.CreateCommand();
        command.CommandText = "select count(*) from pg_class where oid = to_regclass(@relation) and relkind in ('v', 'm')";
        var parameter = command.CreateParameter();
        parameter.ParameterName = "@relation";
        parameter.Value = relation;
        command.Parameters.Add(parameter);
        return Convert.ToInt64(command.ExecuteScalar(), System.Globalization.CultureInfo.InvariantCulture) > 0;
    }
}
