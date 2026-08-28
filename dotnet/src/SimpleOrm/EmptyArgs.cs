namespace SimpleOrm;

/// <summary>The args type for queries and commands that take no parameters.</summary>
public sealed class EmptyArgs
{
    public static EmptyArgs Value { get; } = new();

    private EmptyArgs()
    {
    }
}
