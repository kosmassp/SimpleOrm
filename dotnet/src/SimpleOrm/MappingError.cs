namespace SimpleOrm;

/// <summary>One violation found while loading an <see cref="EntityMap"/>: a stable code, the member or artifact at fault, and what was expected.</summary>
public sealed class MappingError
{
    public MappingError(string code, string target, string message)
    {
        Code = code;
        Target = target;
        Message = message;
    }

    /// <summary>Stable error code from spec/errors.md (e.g. <c>MAP-010</c>). The cross-language contract.</summary>
    public string Code { get; }

    /// <summary>What the error names: <c>Type.Property</c>, an index, or a statement parameter.</summary>
    public string Target { get; }

    public string Message { get; }

    public override string ToString() => $"{Code} {Target}: {Message}";
}
