using System.Text;

namespace SimpleOrm;

/// <summary>One SchemaGuard violation: a stable code, the source (registry entry or entity), and what was expected.</summary>
public sealed class ValidationError(string code, string source, string message)
{
    public string Code { get; } = code;

    public string Source { get; } = source;

    public string Message { get; } = message;

    public override string ToString() => $"{Code} {Source}: {Message}";
}

/// <summary>
/// Thrown by <see cref="SchemaGuard"/> with the complete report — every violation
/// across every registry entry and entity, never just the first (§7.20). Fail fast
/// in all environments; there is no warn-only mode (§7.21).
/// </summary>
public sealed class SchemaValidationException : Exception
{
    public SchemaValidationException(IReadOnlyList<ValidationError> errors)
        : base(BuildMessage(errors))
    {
        Errors = errors;
    }

    public IReadOnlyList<ValidationError> Errors { get; }

    private static string BuildMessage(IReadOnlyList<ValidationError> errors)
    {
        var builder = new StringBuilder()
            .Append("Schema validation failed with ").Append(errors.Count)
            .Append(errors.Count == 1 ? " violation:" : " violations:");
        foreach (var group in errors.GroupBy(e => e.Source))
        {
            builder.AppendLine().Append("  ").Append(group.Key);
            foreach (var error in group)
            {
                builder.AppendLine().Append("    ").Append(error.Code).Append(": ").Append(error.Message);
            }
        }

        return builder.ToString();
    }
}
