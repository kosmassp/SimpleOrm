namespace SimpleOrm.Sample.Models;

/// <summary>
/// Audit columns shared by every sample table. Lives in the sample, not the library:
/// SimpleOrm never requires a base class, but supporting one means the metadata
/// loader must map inherited properties. The <c>[Column]</c> overrides keep the
/// UTC-signaling property names while matching the actual column names
/// (convention alone would produce <c>created_at_utc</c>).
/// </summary>
public abstract class BaseModel
{
    /// <summary>ISO-8601 UTC TEXT in the database (CLAUDE.md §7.9).</summary>
    [Column("created_at")]
    public DateTime CreatedAtUtc { get; set; }

    /// <summary>Null until the row is first updated.</summary>
    [Column("updated_at")]
    public DateTime? UpdatedAtUtc { get; set; }
}
