using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>Entity identity (§7.4): key extraction and equality, incl. composite keys.</summary>
public sealed class EntityIdentityTests
{
    private readonly EntityMapLoader _loader = new();

    [Fact]
    public void Single_key_extraction_and_equality()
    {
        var map = _loader.Load<User>();
        var a = new User { Id = 42, Name = "Ada", Email = "ada@example.com" };
        var b = new User { Id = 42, Name = "Different", Email = "other@example.com" };
        var c = new User { Id = 7, Name = "Ada", Email = "ada@example.com" };

        Assert.Equal([42L], map.GetKeyValues(a));
        Assert.True(map.KeysEqual(a, b));
        Assert.False(map.KeysEqual(a, c));
    }

    [Fact]
    public void Composite_key_extraction_preserves_declaration_order()
    {
        var map = _loader.Load<UserRole>();
        var link = new UserRole { UserId = 1, RoleId = 2 };

        Assert.Equal([1L, 2L], map.GetKeyValues(link));
        Assert.True(map.KeysEqual(link, new UserRole { UserId = 1, RoleId = 2 }));
        Assert.False(map.KeysEqual(link, new UserRole { UserId = 2, RoleId = 1 }));
    }

    [Fact]
    public void Keyless_entities_have_no_identity()
    {
        var map = _loader.Load<DailySales>();
        Assert.Throws<InvalidOperationException>(() => map.GetKeyValues(new DailySales()));
    }
}
