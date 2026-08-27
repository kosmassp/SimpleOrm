namespace SimpleOrm.Sample.Models;

/// <summary>Table <c>users</c> (STRICT). Key: <c>id</c>, database-generated.</summary>
[Table("users")]
public sealed class User : BaseModel
{
    [Key]
    [Generated]
    public long Id { get; set; }

    public required string Name { get; set; }

    public required string Email { get; set; }
}
