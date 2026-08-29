using System.Globalization;
using System.Text;
using System.Text.Json;

namespace SimpleOrm;

/// <summary>
/// A table's shape, provider-agnostic but in **storage types** (what CREATE TABLE
/// emits). Both snapshot producers build this: the metadata exporter
/// (<c>simpleorm snapshot</c>) and the shadow replayer's introspection
/// (<c>simpleorm shadow</c>) — which is what makes the two comparable. Columns and
/// indexes are name-sorted by the writer for determinism.
/// </summary>
public sealed class TableSchema(string name, IReadOnlyList<TableSchema.Column> columns, IReadOnlyList<TableSchema.Index> indexes)
{
    public string Name { get; } = name;

    public IReadOnlyList<Column> Columns { get; } = columns;

    public IReadOnlyList<Index> Indexes { get; } = indexes;

    public sealed class Column(string name, string storageType, bool nullable, bool key = false, bool generated = false)
    {
        public string Name { get; } = name;

        public string StorageType { get; } = storageType;

        public bool Nullable { get; } = nullable;

        public bool Key { get; } = key;

        public bool Generated { get; } = generated;
    }

    public sealed class Index(string name, IReadOnlyList<Index.Part> columns, bool unique = false)
    {
        public string Name { get; } = name;

        public IReadOnlyList<Part> Columns { get; } = columns;

        public bool Unique { get; } = unique;

        public sealed class Part(string columnName, bool descending = false)
        {
            public string ColumnName { get; } = columnName;

            public bool Descending { get; } = descending;
        }
    }
}

/// <summary>
/// The per-table schema snapshot (ADR-0013 add.3, format v2 per ADR-0017): a
/// generated artifact (<c>Migrations/Table/&lt;Object&gt;/V000N.schema.json</c>)
/// recording the table's shape as of a version, stamped with generation time.
/// Storage types make snapshots directly DDL-usable — derived downs, trusted
/// baseline rebuilds — and comparable with database introspection. Tables only —
/// views, statements, and procedures self-reflect.
/// </summary>
public static class SchemaSnapshot
{
    /// <summary>The current model rendered as a schema (the metadata-side producer).</summary>
    public static TableSchema FromMap(EntityMap map, IDialect dialect)
        => new(
            map.RelationName!,
            map.Properties
                .Select(p => new TableSchema.Column(
                    p.ColumnName, dialect.StorageType(p), p.IsNullable, p.IsKey, p.IsGenerated))
                .ToArray(),
            map.Indexes
                .Select(i => new TableSchema.Index(
                    i.Name,
                    i.Columns.Select(c => new TableSchema.Index.Part(c.ColumnName, c.Descending)).ToArray(),
                    i.Unique))
                .ToArray());

    public static string Export(EntityMap map, IDialect dialect, long asOfVersion, DateTimeOffset generatedAt)
        => Export(FromMap(map, dialect), asOfVersion, generatedAt);

    public static string Export(TableSchema schema, long asOfVersion, DateTimeOffset generatedAt)
    {
        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream, new JsonWriterOptions { Indented = true }))
        {
            writer.WriteStartObject();
            writer.WriteString("object", schema.Name);
            writer.WriteNumber("asOfVersion", asOfVersion);
            writer.WriteString("generatedAt", generatedAt.UtcDateTime.ToString("o", CultureInfo.InvariantCulture));

            writer.WritePropertyName("columns");
            writer.WriteStartArray();
            foreach (var column in schema.Columns.OrderBy(c => c.Name, StringComparer.Ordinal))
            {
                writer.WriteStartObject();
                writer.WriteString("column", column.Name);
                writer.WriteString("type", column.StorageType);
                writer.WriteBoolean("nullable", column.Nullable);
                if (column.Key)
                {
                    writer.WriteBoolean("key", true);
                }

                if (column.Generated)
                {
                    writer.WriteBoolean("generated", true);
                }

                writer.WriteEndObject();
            }

            writer.WriteEndArray();

            writer.WritePropertyName("indexes");
            writer.WriteStartArray();
            foreach (var index in schema.Indexes.OrderBy(i => i.Name, StringComparer.Ordinal))
            {
                writer.WriteStartObject();
                writer.WriteString("name", index.Name);
                writer.WritePropertyName("columns");
                writer.WriteStartArray();
                foreach (var part in index.Columns)
                {
                    writer.WriteStartObject();
                    writer.WriteString("column", part.ColumnName);
                    writer.WriteString("direction", part.Descending ? "desc" : "asc");
                    writer.WriteEndObject();
                }

                writer.WriteEndArray();
                if (index.Unique)
                {
                    writer.WriteBoolean("unique", true);
                }

                writer.WriteEndObject();
            }

            writer.WriteEndArray();
            writer.WriteEndObject();
        }

        return Encoding.UTF8.GetString(stream.ToArray());
    }

    public static (TableSchema Schema, long AsOfVersion) Parse(string json)
    {
        using var document = JsonDocument.Parse(json);
        var root = document.RootElement;
        var columns = root.GetProperty("columns").EnumerateArray()
            .Select(c => new TableSchema.Column(
                c.GetProperty("column").GetString()!,
                c.GetProperty("type").GetString()!,
                c.GetProperty("nullable").GetBoolean(),
                c.TryGetProperty("key", out var key) && key.GetBoolean(),
                c.TryGetProperty("generated", out var generated) && generated.GetBoolean()))
            .ToArray();
        var indexes = root.GetProperty("indexes").EnumerateArray()
            .Select(i => new TableSchema.Index(
                i.GetProperty("name").GetString()!,
                i.GetProperty("columns").EnumerateArray()
                    .Select(p => new TableSchema.Index.Part(
                        p.GetProperty("column").GetString()!,
                        p.GetProperty("direction").GetString() == "desc"))
                    .ToArray(),
                i.TryGetProperty("unique", out var unique) && unique.GetBoolean()))
            .ToArray();
        return (
            new TableSchema(root.GetProperty("object").GetString()!, columns, indexes),
            root.GetProperty("asOfVersion").GetInt64());
    }
}
