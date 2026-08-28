namespace SimpleOrm;

/// <summary>Options for a <see cref="Db"/> session.</summary>
public sealed class DbOptions
{
    /// <summary>The database dialect; provides the connection (§7.25).</summary>
    public required IDialect Dialect { get; init; }

    /// <summary>Metadata configuration: naming convention and explicit maps.</summary>
    public MappingOptions Mapping { get; init; } = MappingOptions.Default;
}
