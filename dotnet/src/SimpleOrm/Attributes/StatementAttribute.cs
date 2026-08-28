namespace SimpleOrm;

/// <summary>
/// Maps a class to a custom SQL statement (ADR-0008): the class is the result shape
/// of the query in the referenced <c>.sql</c> embedded resource (path relative to
/// <c>Sql/</c>, per CLAUDE.md §7.5 — SQL lives in files, never inline in attributes).
/// A class carries exactly one relation source — <see cref="TableAttribute"/>,
/// <see cref="ViewAttribute"/>, <see cref="MaterializedViewAttribute"/>,
/// <see cref="StatementAttribute"/>, or <see cref="ProcedureAttribute"/>.
///
/// Statement-backed entities are read-only and keyless at Level 1 (<c>[Key]</c>,
/// <c>[Generated]</c>, <c>[Version]</c>, and <c>[Index]</c> are loader errors).
/// SchemaGuard validates the class by preparing the statement and matching its
/// result columns against the mapped properties, without needing a registry entry.
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class StatementAttribute : Attribute
{
    public StatementAttribute(string sqlPath) => SqlPath = sqlPath;

    /// <summary>Embedded-resource path of the .sql file, relative to <c>Sql/</c>.</summary>
    public string SqlPath { get; }
}
