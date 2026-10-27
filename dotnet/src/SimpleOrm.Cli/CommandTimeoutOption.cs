using System.Globalization;
using SimpleOrm;

namespace SimpleOrm.Cli;

/// <summary>
/// The CLI's <c>--command-timeout &lt;seconds&gt;</c> (ADR-0047): the session's
/// <see cref="DbOptions.CommandTimeout"/> for every command that opens a database —
/// long DDL on a network engine outruns the driver's default (SqlClient: 30 s).
/// </summary>
public static class CommandTimeoutOption
{
    /// <summary>Null when absent (the driver's default); a whole positive number of seconds otherwise; anything else refuses.</summary>
    public static TimeSpan? Parse(string? raw)
    {
        if (raw is null)
        {
            return null;
        }

        if (!int.TryParse(raw, NumberStyles.None, CultureInfo.InvariantCulture, out var seconds) || seconds < 1)
        {
            throw new SimpleOrmException(
                "CLI", "arguments", $"--command-timeout '{raw}': expected a whole number of seconds, at least 1");
        }

        return TimeSpan.FromSeconds(seconds);
    }
}
