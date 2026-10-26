namespace SimpleOrm;

/// <summary>Options for a <see cref="Db"/> session.</summary>
public sealed class DbOptions
{
    /// <summary>The database dialect; provides the connection (§7.25).</summary>
    public required IDialect Dialect { get; init; }

    /// <summary>Metadata configuration: naming convention and explicit maps.</summary>
    public MappingOptions Mapping { get; init; } = MappingOptions.Default;

    /// <summary>Custom and JSON type handlers (§7.9/§7.10); the extension point beyond the fixed conversion table.</summary>
    public TypeHandlerRegistry TypeHandlers { get; init; } = new();

    /// <summary>
    /// Whether the session keeps an identity map and change snapshots (Level 3 M1,
    /// ADR-0034 add.1). <see cref="TrackingMode.Untracked"/> by default — every
    /// Level 0–2 behaviour is unchanged until a session opts in.
    /// </summary>
    public TrackingMode Tracking { get; init; } = TrackingMode.Untracked;

    /// <summary>
    /// The session clock (Level 3 M3, spec/hooks.md): what <c>[CreatedAt]</c> fills
    /// and <c>[UpdatedAt]</c> stamps. Must return UTC (<c>DateTimeKind.Utc</c>,
    /// §7.9); <c>DateTime.UtcNow</c> by default, fixed by tests and the
    /// conformance runners.
    /// </summary>
    public Func<DateTime> Clock { get; init; } = () => DateTime.UtcNow;

    /// <summary>
    /// Statement batching (Level 4, ADR-0044, spec/session.md "Batching"):
    /// <see cref="BatchingMode.Off"/> by default — one round trip per statement,
    /// the Level 0–3 behaviour. <see cref="BatchingMode.Auto"/> sends the
    /// statements of one call (<see cref="Db.FlushAsync"/>, <see cref="StatementBatch.SendAsync"/>)
    /// in as few messages as the plan allows; nothing waits and nothing is
    /// deferred past the call.
    /// </summary>
    public BatchingMode Batching { get; init; } = BatchingMode.Off;

    /// <summary>The most statements one message carries under <see cref="BatchingMode.Auto"/>; 100 by default, at least 1.</summary>
    public int MaxStatementsPerMessage { get; init; } = 100;

    /// <summary>
    /// The statement log (Level 4 item 4, ADR-0045, spec/session.md "Statement
    /// log"): called once per statement, as it is sent, with one line of text —
    /// the operation's name, the SQL exactly as sent, and the parameter names.
    /// Forward it to the application's logger (<c>line => logger.LogDebug(line)</c>)
    /// or use <see cref="DbLog.Console"/>; unset (the default), nothing is written.
    /// </summary>
    public Action<string>? Log { get; init; }

    /// <summary>
    /// Whether the logged line carries the parameter values beside their names.
    /// Off by default: values may be personal data, and a log never receives
    /// them unless the application asks.
    /// </summary>
    public bool LogParameterValues { get; init; }
}

/// <summary>
/// The session's batching mode (ADR-0044): <see cref="Off"/> sends every
/// statement as its own message; <see cref="Auto"/> groups the statements one
/// call already holds — a flush, a batch send — by the plan of spec/session.md
/// "Batching". No timer, no implicit queue: a lone statement runs as always.
/// </summary>
public enum BatchingMode
{
    Off,
    Auto,
}

/// <summary>
/// The session's tracking mode (ADR-0034 add.1): <see cref="Tracked"/> keeps one
/// instance per (entity type, key) for the session's lifetime and a snapshot of
/// each instance's mapped values as read, so a re-read returns the same instance
/// (the tracked one wins; <c>RefreshAsync</c> overwrites it on request) and
/// <c>UpdateAsync</c> writes only what changed. <see cref="Untracked"/> is the
/// Level 0–2 session: every read materializes a fresh instance.
/// </summary>
public enum TrackingMode
{
    Untracked,
    Tracked,
}
