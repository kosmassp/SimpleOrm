using System.Globalization;
using System.Text;
using System.Text.Json;

namespace SimpleOrm;

/// <summary>
/// The per-table schema snapshot (ADR-0013 addendum 3): a generated artifact in the
/// table's migration folder (<c>Migrations/Table/&lt;Object&gt;/schema.json</c>)
/// recording the table's shape as of a version, stamped with generation time. The
/// diff generator compares entity metadata against these instead of replaying
/// history. Tables only — views, statements, and procedures self-reflect through
/// their attribute SQL. Produced by <c>simpleorm snapshot</c>.
/// </summary>
public static class SchemaSnapshot
{
    public static string Export(EntityMap map, long asOfVersion, DateTimeOffset generatedAt)
    {
        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream, new JsonWriterOptions { Indented = true }))
        {
            writer.WriteStartObject();
            writer.WriteString("object", map.RelationName);
            writer.WriteNumber("asOfVersion", asOfVersion);
            writer.WriteString("generatedAt", generatedAt.UtcDateTime.ToString("o", CultureInfo.InvariantCulture));
            EntityMapJson.WriteColumns(writer, map);
            EntityMapJson.WriteIndexes(writer, map);
            writer.WriteEndObject();
        }

        return Encoding.UTF8.GetString(stream.ToArray());
    }
}
