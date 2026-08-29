using System.Text;
using System.Text.Json;

namespace SimpleOrm;

/// <summary>
/// Exports an <see cref="EntityMap"/> as the conformance JSON defined in
/// spec/metadata-model.md. The export is deliberately column-centric and
/// language-neutral: column names, neutral type tokens, and SQL-side parameter
/// names — never CLR property names, which differ per implementation language.
/// Every port must produce byte-identical output for the same entity.
/// </summary>
public static class EntityMapJson
{
    public static string Export(EntityMap map)
    {
        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream, new JsonWriterOptions { Indented = true }))
        {
            writer.WriteStartObject();
            writer.WriteString("entity", map.EntityType.Name);

            writer.WritePropertyName("source");
            writer.WriteStartObject();
            writer.WriteString("kind", KindToken(map.Kind));
            if (map.Kind == RelationKind.Statement)
            {
                writer.WriteString("sql", NormalizeSql(map.DefiningSql!));
                writer.WritePropertyName("parameters");
                writer.WriteStartArray();
                foreach (var parameter in map.StatementParameters)
                {
                    writer.WriteStartObject();
                    writer.WriteString("name", parameter.Name);
                    writer.WriteString("type", TypeToken(parameter.ClrType, enumAsInt: false));
                    writer.WriteEndObject();
                }

                writer.WriteEndArray();
            }
            else
            {
                writer.WriteString("name", map.RelationName);
                if (map.Schema is not null)
                {
                    writer.WriteString("schema", map.Schema);
                }

                if (map.DefiningSql is not null)
                {
                    writer.WriteString("sql", NormalizeSql(map.DefiningSql));
                }

                if (map.Kind == RelationKind.Procedure)
                {
                    writer.WritePropertyName("parameters");
                    writer.WriteStartArray();
                    foreach (var parameter in map.StatementParameters)
                    {
                        writer.WriteStartObject();
                        writer.WriteString("name", parameter.Name);
                        writer.WriteString("type", TypeToken(parameter.ClrType, enumAsInt: false));
                        writer.WriteEndObject();
                    }

                    writer.WriteEndArray();
                }
            }

            writer.WriteEndObject();

            writer.WritePropertyName("key");
            writer.WriteStartObject();
            writer.WriteString("strategy", StrategyToken(map.KeyStrategy));
            writer.WritePropertyName("columns");
            writer.WriteStartArray();
            foreach (var key in map.KeyProperties)
            {
                writer.WriteStringValue(key.ColumnName);
            }

            writer.WriteEndArray();
            writer.WriteEndObject();

            if (map.VersionProperty is not null)
            {
                writer.WriteString("version", map.VersionProperty.ColumnName);
            }

            WriteColumns(writer, map);
            WriteIndexes(writer, map);

            if (map.Relationships.Count > 0)
            {
                writer.WritePropertyName("relationships");
                writer.WriteStartArray();
                foreach (var relationship in map.Relationships)
                {
                    var foreignKey = map.Properties.First(p => p.PropertyName == relationship.ForeignKeyProperty);
                    writer.WriteStartObject();
                    writer.WriteString("kind", "many_to_one");
                    writer.WriteString("foreignKeyColumn", foreignKey.ColumnName);
                    writer.WriteString("references", relationship.TargetType.Name);
                    writer.WriteEndObject();
                }

                writer.WriteEndArray();
            }

            writer.WriteEndObject();
        }

        return Encoding.UTF8.GetString(stream.ToArray());
    }

    internal static void WriteColumns(Utf8JsonWriter writer, EntityMap map)
    {
        writer.WritePropertyName("columns");
        writer.WriteStartArray();
        foreach (var property in map.Properties)
        {
            writer.WriteStartObject();
            writer.WriteString("column", property.ColumnName);
            writer.WriteString("type", TypeToken(property.ClrType, property.EnumAsInt));
            writer.WriteBoolean("nullable", property.IsNullable);
            if (property.IsKey)
            {
                writer.WriteBoolean("key", true);
            }

            if (property.IsGenerated)
            {
                writer.WriteBoolean("generated", true);
            }

            writer.WriteEndObject();
        }

        writer.WriteEndArray();
    }

    internal static void WriteIndexes(Utf8JsonWriter writer, EntityMap map)
    {
        if (map.Indexes.Count == 0)
        {
            return;
        }

        writer.WritePropertyName("indexes");
        writer.WriteStartArray();
        foreach (var index in map.Indexes)
        {
            writer.WriteStartObject();
            writer.WriteString("name", index.Name);
            writer.WritePropertyName("columns");
            writer.WriteStartArray();
            foreach (var column in index.Columns)
            {
                writer.WriteStartObject();
                writer.WriteString("column", column.ColumnName);
                writer.WriteString("direction", column.Descending ? "desc" : "asc");
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
    }

    private static string KindToken(RelationKind kind) => kind switch
    {
        RelationKind.Table => "table",
        RelationKind.View => "view",
        RelationKind.MaterializedView => "materialized_view",
        RelationKind.Statement => "statement",
        RelationKind.Procedure => "procedure",
        _ => throw new ArgumentOutOfRangeException(nameof(kind)),
    };

    private static string StrategyToken(KeyStrategy strategy) => strategy switch
    {
        KeyStrategy.None => "none",
        KeyStrategy.DatabaseGenerated => "database_generated",
        KeyStrategy.ClientGuid => "client_guid",
        KeyStrategy.Natural => "natural",
        _ => throw new ArgumentOutOfRangeException(nameof(strategy)),
    };

    /// <summary>
    /// Neutral type tokens (spec/metadata-model.md): the cross-language vocabulary.
    /// Unknown types export as <c>clr:&lt;full name&gt;</c> and require a handler.
    /// </summary>
    private static string TypeToken(Type type, bool enumAsInt)
    {
        var underlying = Nullable.GetUnderlyingType(type) ?? type;
        if (underlying.IsEnum)
        {
            return enumAsInt ? "enum_int" : "enum_text";
        }

        if (underlying == typeof(int))
        {
            return "int32";
        }

        if (underlying == typeof(long))
        {
            return "int64";
        }

        if (underlying == typeof(short))
        {
            return "int16";
        }

        if (underlying == typeof(decimal))
        {
            return "decimal";
        }

        if (underlying == typeof(double))
        {
            return "double";
        }

        if (underlying == typeof(float))
        {
            return "float";
        }

        if (underlying == typeof(bool))
        {
            return "bool";
        }

        if (underlying == typeof(string))
        {
            return "string";
        }

        if (underlying == typeof(Guid))
        {
            return "guid";
        }

        if (underlying == typeof(byte[]))
        {
            return "bytes";
        }

        if (underlying == typeof(DateTime))
        {
            return "datetime";
        }

        if (underlying == typeof(DateTimeOffset))
        {
            return "datetimeoffset";
        }

        if (underlying == typeof(TimeSpan))
        {
            return "time";
        }

        return underlying.FullName switch
        {
            "System.DateOnly" => "date",
            "System.TimeOnly" => "time",
            _ => "clr:" + underlying.FullName,
        };
    }

    /// <summary>Collapses whitespace so the exported SQL is layout-independent across implementations.</summary>
    private static string NormalizeSql(string sql)
        => string.Join(" ", sql.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries));
}
