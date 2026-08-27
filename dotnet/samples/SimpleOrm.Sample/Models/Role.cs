namespace SimpleOrm.Sample.Models;

/// <summary>Table <c>roles</c> (STRICT). Key: <c>id</c>, database-generated.</summary>
[Table("roles")]
public sealed class Role : BaseModel
{
    [Key]
    [Generated]
    public long Id { get; set; }

    public required string Name { get; set; }
}
