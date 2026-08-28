namespace SimpleOrm;

/// <summary>
/// Sort direction token for <see cref="IndexAttribute"/> columns: follows the column
/// it applies to, e.g. <c>[Index(nameof(Status), nameof(CreatedAtUtc), SortOrder.Desc)]</c>.
/// A column with no token is ascending.
/// </summary>
public enum SortOrder
{
    Asc,
    Desc,
}
