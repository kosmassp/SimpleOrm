namespace SimpleOrm.Sample.Models;

/// <summary>Table <c>roles</c> (STRICT). Key: <c>id</c>, database-generated.</summary>
[Table("roles")]
public sealed class Role : BaseModel
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    public required string Name { get; set; }
}
