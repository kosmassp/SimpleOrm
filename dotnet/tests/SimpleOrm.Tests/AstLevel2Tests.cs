using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Level 2 AST refusals the conformance cases cannot express (ADR-0031, raised by
/// the Go port): a join hanging off an undeclared parent alias, and a subquery
/// membership whose arity disagrees with its subquery's projection — both
/// <c>QRY-006</c> before rendering, never an index or database error.
/// </summary>
public sealed class AstLevel2Tests
{
    private static string Bind(object? value, PropertyMap? property) => "@c0";

    [Fact]
    public void Join_off_an_undeclared_parent_alias_is_QRY006()
    {
        var loader = new EntityMapLoader();
        var users = loader.Load<User>();
        var roles = loader.Load<Role>();
        var ast = new SelectAst(users, [], [], joins:
        [
            new SelectJoin(roles, "j0", parentAlias: "l0", [("RoleId", "Id")], project: true),
        ]);

        var exception = Assert.Throws<SimpleOrmException>(() => new SqliteDialect().SelectSql(ast, Bind));
        Assert.Equal("QRY-006", exception.Code);
        Assert.Contains("l0", exception.Message);
    }

    [Fact]
    public void Membership_arity_mismatch_is_QRY006_on_every_dialect()
    {
        var loader = new EntityMapLoader();
        var links = loader.Load<UserRole>();
        var subquery = new SelectAst(links, [], [], projection: [links.Properties.Single(p => p.PropertyName == "UserId")]);
        var ast = new SelectAst(links, [Criteria.InSelect(["UserId", "RoleId"], subquery)], []);

        foreach (IDialect dialect in new IDialect[] { new SqliteDialect(), new SqlServerDialect() })
        {
            var exception = Assert.Throws<SimpleOrmException>(() => dialect.SelectSql(ast, Bind));
            Assert.Equal("QRY-006", exception.Code);
        }
    }
}
