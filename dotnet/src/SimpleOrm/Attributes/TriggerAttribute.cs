namespace SimpleOrm;

/// <summary>When a trigger fires relative to the statement (ADR-0042). The dialect renders what it has; a form it lacks is <c>DDL-005</c>.</summary>
public enum TriggerTiming
{
    /// <summary>Before the row is written (SQLite, PostgreSQL; SQL Server has no BEFORE — <c>DDL-005</c>).</summary>
    Before,

    /// <summary>After the row is written (every dialect).</summary>
    After,

    /// <summary>In place of the write: on a view everywhere; on a table only SQL Server (<c>DDL-005</c> elsewhere).</summary>
    InsteadOf,
}

/// <summary>The statement events a trigger fires on (ADR-0042); combinable. SQLite renders one trigger per event.</summary>
[Flags]
public enum TriggerEvent
{
    None = 0,
    Insert = 1,
    Update = 2,
    Delete = 4,
}

/// <summary>
/// Triggers in the mapping (Level 4, ADR-0042). Two placements of one attribute:
///
/// On the <b>trigger class</b> — the declaration, like <see cref="MaterializedViewAttribute"/>
/// carries a view's SQL: the trigger name, timing, events, and <b>one</b> body.
/// The body is set-based SQL over the changed rows, portable across dialects
/// because the only database-specific parts are three tokens the dialect
/// expands: <c>{new: col, col}</c> and <c>{old: col, col}</c> (the inserted/updated
/// and the deleted/pre-update rows as a relation — <c>inserted</c>/<c>deleted</c>
/// on SQL Server, a one-row derived table over <c>new.</c>/<c>old.</c> on SQLite
/// and PostgreSQL) and <c>{now}</c> (UTC now in the §7.9 storage form). Everything
/// around the body — <c>set nocount on</c>, <c>begin/end</c>, the PostgreSQL
/// function and its <c>return</c>, the granularity — is the dialect's rendering.
/// Each statement of the body ends with <c>;</c>. The class is a declaration
/// object, never an entity: no columns, no key; scans skip it.
///
/// On the <b>entity</b> — the link: <c>[Trigger(typeof(RoleAuditTrigger))]</c>,
/// repeatable (a table carries many triggers; a trigger belongs to one table).
/// Legal on <c>[Table]</c> entities, and on <c>[View]</c> entities for
/// <see cref="TriggerTiming.InsteadOf"/> only. Every misplaced or malformed
/// declaration is <c>MAP-029</c>.
/// </summary>
[AttributeUsage(AttributeTargets.Class, AllowMultiple = true)]
public sealed class TriggerAttribute : Attribute
{
    /// <summary>The link, on an entity: this entity's table carries the trigger declared by <paramref name="trigger"/>.</summary>
    public TriggerAttribute(Type trigger) => Trigger = trigger;

    /// <summary>The declaration, on a trigger class: name, timing, events, and the one neutral body.</summary>
    public TriggerAttribute(string name, TriggerTiming timing, TriggerEvent events, string sql)
    {
        Name = name;
        Timing = timing;
        Events = events;
        Sql = sql;
    }

    /// <summary>The linked trigger class (link form); null on a declaration.</summary>
    public Type? Trigger { get; }

    /// <summary>The trigger name exactly as it exists in the database (declaration form); null on a link.</summary>
    public string? Name { get; }

    public TriggerTiming Timing { get; }

    public TriggerEvent Events { get; }

    /// <summary>The body, verbatim: set-based SQL with the <c>{new: …}</c>, <c>{old: …}</c>, and <c>{now}</c> tokens.</summary>
    public string? Sql { get; }

    /// <summary>
    /// Declaration form, optional: narrows an <see cref="TriggerEvent.Update"/> to
    /// these properties of the entity (resolved to columns through its mapping):
    /// <c>UPDATE OF</c> on SQLite and PostgreSQL, an <c>if not (update(…))</c>
    /// prologue on SQL Server.
    /// </summary>
    public string[]? UpdateOf { get; set; }
}
