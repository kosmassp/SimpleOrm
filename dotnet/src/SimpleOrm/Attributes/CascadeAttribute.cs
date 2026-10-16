namespace SimpleOrm;

/// <summary>
/// The unit-of-work operations a navigation cascades (Level 3 M2, ADR-0034 §7c):
/// <see cref="Persist"/> schedules the navigation's targets with the owner,
/// <see cref="Remove"/> removes them with it. Flags; <see cref="All"/> is both.
/// </summary>
[Flags]
public enum CascadeOperations
{
    None = 0,
    Persist = 1,
    Remove = 2,
    All = Persist | Remove,
}

/// <summary>
/// Declares what a navigation cascades (Level 3 M2). Cascades are <b>declared,
/// never inferred</b> and per operation: a <c>[ManyToOne]</c>, <c>[OneToOne]</c>,
/// or <c>[OneToMany]</c> navigation may carry it; a <c>[ManyToMany]</c>
/// navigation may not (its link rows are entities the unit of work persists
/// explicitly) and a non-navigation property may not — both <c>MAP-025</c>.
/// Cascading never loads: a remove cascading over an unloaded collection is
/// <c>UOW-005</c>, a persist cascades only over initialized navigations.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class CascadeAttribute(CascadeOperations operations) : Attribute
{
    public CascadeOperations Operations { get; } = operations;
}
