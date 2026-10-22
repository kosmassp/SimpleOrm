using System.Text.RegularExpressions;
using Microsoft.Data.Sqlite;

namespace SimpleOrm.Sqlite;

/// <summary>
/// The inverse of <see cref="SqliteDialect.CreateTriggerSql"/> for the shadow
/// replayer (ADR-0042): the stored create text of a trigger parses back to the
/// neutral declaration — header fields from the dialect's fixed grammar, the
/// body with the dialect's expansions folded back into the tokens
/// (<c>(select new.a as a, new.b as b)</c> → <c>{new: a, b}</c>, the UTC
/// <c>strftime</c> → <c>{now}</c>). A multi-event declaration's per-event
/// triggers (<c>&lt;name&gt;_&lt;event&gt;</c>) merge back into one. Tooling-only,
/// SQLite-only: the shadow replays into SQLite, and the dialect that wrote the
/// text is the one that reads it. A text this grammar does not match (a trigger
/// written by hand) yields null and the snapshot is skipped with a note.
/// </summary>
public static class SqliteTriggerDdl
{
    private static readonly Regex Header = new(
        @"^create\s+trigger\s+(?<name>\S+)\s+(?<timing>before|after|instead\s+of)\s+(?<event>insert|update|delete)(\s+of\s+(?<cols>[^\n]+?))?\s+on\s+(?<relation>\S+)\s+for\s+each\s+row\s+begin\s+(?<body>[\s\S]*?)\s*end\s*$",
        RegexOptions.Compiled | RegexOptions.IgnoreCase);

    private static readonly Regex RowSelect = new(
        @"\(select\s+(?<row>new|old)\.(?<first>\w+)\s+as\s+\w+(?<rest>(\s*,\s*(new|old)\.\w+\s+as\s+\w+)*)\)",
        RegexOptions.Compiled | RegexOptions.IgnoreCase);

    private static readonly Regex Column = new(@"(new|old)\.(?<col>\w+)\s+as", RegexOptions.Compiled | RegexOptions.IgnoreCase);

    private const string NowExpansion = "strftime('%Y-%m-%dT%H:%M:%fZ', 'now')";

    /// <summary>Reads the trigger declared under <paramref name="name"/> (its per-event forms included) from the shadow database, or null when absent or hand-written.</summary>
    public static TriggerSnapshot? Read(SqliteConnection connection, string name)
    {
        var texts = new List<string>();
        using (var command = connection.CreateCommand())
        {
            command.CommandText = new SqliteDialect().TriggerDefinitionSql;
            command.Parameters.AddWithValue("@relation", name);
            using var reader = command.ExecuteReader();
            while (reader.Read())
            {
                texts.Add(reader.GetString(0));
            }
        }

        if (texts.Count == 0)
        {
            return null;
        }

        var parsed = texts.Select(Parse).ToArray();
        if (parsed.Any(p => p is null))
        {
            return null;
        }

        var first = parsed[0]!.Value;
        var events = TriggerEvent.None;
        foreach (var part in parsed)
        {
            events |= part!.Value.Event;
        }

        var onView = IsView(connection, first.Relation);
        return new TriggerSnapshot(
            first.Relation, onView,
            new TriggerDefinition(name, first.Timing, events, first.Body, first.UpdateOf));
    }

    /// <summary>One stored trigger text → its header and un-expanded body; null when the grammar does not match.</summary>
    public static (string Relation, TriggerTiming Timing, TriggerEvent Event, string Body, IReadOnlyList<string> UpdateOf)? Parse(string sql)
    {
        var match = Header.Match(sql.Trim());
        if (!match.Success)
        {
            return null;
        }

        var timing = match.Groups["timing"].Value.ToLowerInvariant() switch
        {
            "before" => TriggerTiming.Before,
            "after" => TriggerTiming.After,
            _ => TriggerTiming.InsteadOf,
        };
        var e = match.Groups["event"].Value.ToLowerInvariant() switch
        {
            "insert" => TriggerEvent.Insert,
            "update" => TriggerEvent.Update,
            _ => TriggerEvent.Delete,
        };
        var updateOf = match.Groups["cols"].Success
            ? match.Groups["cols"].Value.Split(',').Select(c => c.Trim()).ToArray()
            : [];
        var body = RowSelect.Replace(match.Groups["body"].Value, m =>
            "{" + m.Groups["row"].Value.ToLowerInvariant() + ": "
            + string.Join(", ", Column.Matches(m.Value).Cast<Match>().Select(c => c.Groups["col"].Value)) + "}");
        body = Regex.Replace(body, Regex.Escape(NowExpansion), "{now}", RegexOptions.IgnoreCase);
        return (match.Groups["relation"].Value, timing, e, body, updateOf);
    }

    private static bool IsView(SqliteConnection connection, string relation)
    {
        using var command = connection.CreateCommand();
        command.CommandText = "select count(*) from sqlite_master where type = 'view' and name = @name";
        command.Parameters.AddWithValue("@name", relation);
        return (long)command.ExecuteScalar()! > 0;
    }
}
