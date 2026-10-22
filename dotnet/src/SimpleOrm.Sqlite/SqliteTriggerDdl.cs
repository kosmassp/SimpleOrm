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

    private static readonly Regex ColumnPair = new(@"(?<row>new|old)\.(?<col>\w+)\s+as\s+(?<alias>\w+)", RegexOptions.Compiled | RegexOptions.IgnoreCase);

    private const string NowExpansion = "strftime('%Y-%m-%dT%H:%M:%fZ', 'now')";

    /// <summary>Reads the trigger declared under <paramref name="name"/> (its per-event forms included) from the shadow database, or null when absent or hand-written.</summary>
    public static TriggerSnapshot? Read(SqliteConnection connection, string name)
        => Read(connection, name, out _);

    /// <summary>
    /// As <see cref="Read(SqliteConnection, string)"/>; <paramref name="note"/> says why a trigger that exists
    /// was not read back (a hand-written text, or per-event triggers that do not form one declaration) and is
    /// null when the trigger is absent or read.
    /// </summary>
    public static TriggerSnapshot? Read(SqliteConnection connection, string name, out string? note)
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

        note = null;
        if (texts.Count == 0)
        {
            return null;   // absent: dropped at this version
        }

        var parsed = texts.Select(Parse).ToArray();
        if (parsed.Any(p => p is null))
        {
            note = $"trigger {name}: a stored definition does not match the dialect's grammar (hand-written?); skipped";
            return null;
        }

        // The per-event triggers of one declaration share the relation, the timing, and the body, and carry
        // the names the rendering gives them; anything else was not written from one declaration.
        var first = parsed[0]!.Value;
        var events = TriggerEvent.None;
        IReadOnlyList<string> updateOf = [];
        foreach (var part in parsed.Select(p => p!.Value))
        {
            var expectedName = SqliteDialect.TriggerObjectName(name, part.Event, parsed.Length);
            if (!string.Equals(part.Name, expectedName, StringComparison.OrdinalIgnoreCase)
                || (events & part.Event) != 0
                || part.Timing != first.Timing
                || !string.Equals(part.Relation, first.Relation, StringComparison.OrdinalIgnoreCase)
                || new TriggerDefinition(name, part.Timing, part.Event, part.Body).NormalizedSql
                    != new TriggerDefinition(name, first.Timing, first.Event, first.Body).NormalizedSql)
            {
                note = $"trigger {name}: the stored per-event triggers do not form one declaration; skipped";
                return null;
            }

            events |= part.Event;
            if (part.Event == TriggerEvent.Update)
            {
                updateOf = part.UpdateOf;   // the narrowing lives on the update trigger alone, whatever the name order
            }
        }

        var onView = IsView(connection, first.Relation);
        return new TriggerSnapshot(
            first.Relation, onView,
            new TriggerDefinition(name, first.Timing, events, first.Body, updateOf));
    }

    /// <summary>One stored trigger text → its header and un-expanded body; null when the grammar does not match.</summary>
    public static (string Relation, TriggerTiming Timing, TriggerEvent Event, string Body, IReadOnlyList<string> UpdateOf, string Name)? Parse(string sql)
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
        var body = RowSelect.Replace(match.Groups["body"].Value, FoldRowSelect);
        body = Regex.Replace(body, Regex.Escape(NowExpansion), "{now}", RegexOptions.IgnoreCase);
        return (match.Groups["relation"].Value, timing, e, body, updateOf, match.Groups["name"].Value);
    }

    /// <summary>
    /// <c>(select new.a as a, new.b as b)</c> → <c>{new: a, b}</c> — only the exact expansion: one row
    /// variable throughout and every alias its column. Anything else is the author's SQL and stays as written.
    /// </summary>
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

    private static bool IsView(SqliteConnection connection, string relation)
    {
        using var command = connection.CreateCommand();
        command.CommandText = "select count(*) from sqlite_master where type = 'view' and name = @name";
        command.Parameters.AddWithValue("@name", relation);
        return (long)command.ExecuteScalar()! > 0;
    }
}
