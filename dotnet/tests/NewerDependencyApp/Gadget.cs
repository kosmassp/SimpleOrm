using SimpleOrm;

namespace NewerDependencyApp;

/// <summary>Table <c>gadgets</c>: the entity the CLI must still see through the isolated load context.</summary>
[Table("gadgets")]
public sealed class Gadget
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    public required string Name { get; set; }
}
