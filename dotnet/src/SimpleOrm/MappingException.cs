using System.Text;

namespace SimpleOrm;

/// <summary>
/// Thrown when loading an <see cref="EntityMap"/> finds violations. Carries every
/// violation found for the type — loaders never stop at the first error.
/// </summary>
public sealed class MappingException : Exception
{
    public MappingException(Type entityType, IReadOnlyList<MappingError> errors)
        : base(BuildMessage(entityType, errors))
    {
        EntityType = entityType;
        Errors = errors;
    }

    public Type EntityType { get; }

    public IReadOnlyList<MappingError> Errors { get; }

    private static string BuildMessage(Type entityType, IReadOnlyList<MappingError> errors)
    {
        var builder = new StringBuilder()
            .Append("Mapping of '").Append(entityType.FullName).Append("' failed with ")
            .Append(errors.Count).Append(errors.Count == 1 ? " error:" : " errors:");
        foreach (var error in errors)
        {
            builder.AppendLine().Append("  ").Append(error);
        }

        return builder.ToString();
    }
}
