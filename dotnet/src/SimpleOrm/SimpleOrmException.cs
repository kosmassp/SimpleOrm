namespace SimpleOrm;

/// <summary>
/// A runtime failure with a stable error code from spec/errors.md (parameter
/// binding, query execution, transactions). Metadata loading uses
/// <see cref="MappingException"/>, which aggregates.
/// </summary>
public sealed class SimpleOrmException : Exception
{
    public SimpleOrmException(string code, string target, string message)
        : base($"{code} {target}: {message}")
    {
        Code = code;
        Target = target;
    }

    /// <summary>Stable code, e.g. <c>PRM-001</c> or <c>QRY-002</c>.</summary>
    public string Code { get; }

    /// <summary>What the error names: a query, parameter, or session.</summary>
    public string Target { get; }
}
