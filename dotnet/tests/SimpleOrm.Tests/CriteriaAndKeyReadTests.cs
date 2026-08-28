using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>ADR-0012: key reads (GetAsync/GetOrDefaultAsync) and the criteria query core.</summary>
[Collection(SqliteCollection.Name)]
public sealed class CriteriaAndKeyReadTests(SqliteFixture fixture)
{
    [Fact]
    public async Task Get_by_key_reads_and_misses_with_codes()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        Assert.Equal("Ada", (await db.GetAsync<User>(ada.Id, CancellationToken.None)).Name);
        Assert.Equal("Ada", (await db.GetAsync<User>((int)ada.Id, CancellationToken.None)).Name);   // int widens to long

        var missing = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.GetAsync<User>(999_999, CancellationToken.None));
        Assert.Equal("CRUD-001", missing.Code);
        Assert.Null(await db.GetOrDefaultAsync<User>(999_999, CancellationToken.None));
    }

    [Fact]
    public async Task Composite_keys_pass_tuples_and_validate_shape()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        var role = new Role { Name = "admin", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(role, CancellationToken.None);
        await db.InsertAsync(
            new UserRole { UserId = ada.Id, RoleId = role.Id, CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);

        var link = await db.GetAsync<UserRole>((ada.Id, role.Id), CancellationToken.None);
        Assert.Equal(role.Id, link.RoleId);

        var arity = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.GetAsync<UserRole>(ada.Id, CancellationToken.None));
        Assert.Equal("CRUD-002", arity.Code);

        var type = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.GetAsync<User>("not a key", CancellationToken.None));
        Assert.Equal("CRUD-002", type.Code);

        var keyless = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.GetAsync<DailySales>(1, CancellationToken.None));
        Assert.Equal("QRY-005", keyless.Code);
    }

    [Fact]
    public async Task Owner_shape_or_and_in_with_implicit_and()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        await TestDb.InsertUserAsync(db, "Edsger", "edsger@example.com");

        // (Id = ada OR Name IN ('Grace','Nope')) AND CreatedAtUtc >= seed-1day
        var users = await db.Query<User>()
            .Where(
                Criteria.Or(
                    Criteria.Eq(nameof(User.Id), ada.Id),
                    Criteria.In(nameof(User.Name), "Grace", "Nope")),
                Criteria.Ge(nameof(User.CreatedAtUtc), TestDb.SeedTime.AddDays(-1)))
            .OrderBy(nameof(User.Name))
            .ToListAsync(CancellationToken.None);

        Assert.Equal(["Ada", "Grace"], users.Select(u => u.Name));
    }

    [Fact]
    public async Task Ordering_paging_null_checks_and_like()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        await TestDb.InsertUserAsync(db, "Edsger", "edsger@example.com");

        var page = await db.Query<User>()
            .OrderBy(nameof(User.Name), SortOrder.Desc)
            .Limit(2)
            .Offset(1)
            .ToListAsync(CancellationToken.None);
        Assert.Equal(["Edsger", "Ada"], page.Select(u => u.Name));

        var fresh = await db.Query<User>()
            .Where(Criteria.IsNull(nameof(User.UpdatedAtUtc)))
            .ToListAsync(CancellationToken.None);
        Assert.Equal(3, fresh.Count);

        var gr = await db.Query<User>()
            .Where(Criteria.Like(nameof(User.Name), "Gr%"))
            .ToListAsync(CancellationToken.None);
        Assert.Equal("Grace", Assert.Single(gr).Name);

        var none = await db.Query<User>()
            .Where(Criteria.In<long>(nameof(User.Id), []))
            .ToListAsync(CancellationToken.None);
        Assert.Empty(none);
    }

    [Fact]
    public async Task Unknown_property_is_QRY006_and_statements_are_QRY005()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var unknown = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.Query<User>().Where(Criteria.Eq("Nope", 1)).ToListAsync(CancellationToken.None));
        Assert.Equal("QRY-006", unknown.Code);

        var statement = Assert.Throws<SimpleOrmException>(() => db.Query<DailySales>());
        Assert.Equal("QRY-005", statement.Code);
    }

    [Fact]
    public async Task Criteria_work_on_views_too()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        await db.InsertAsync(
            new Transaction { UserId = ada.Id, Status = TransactionStatus.Completed, Amount = 10m, CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);

        var busy = await db.Query<UserTransactionTotal>()
            .Where(Criteria.Gt(nameof(UserTransactionTotal.TransactionCount), 0))
            .ToListAsync(CancellationToken.None);

        Assert.Equal("Ada", Assert.Single(busy).UserName);

        var viaKey = await db.GetAsync<UserTransactionTotal>(ada.Id, CancellationToken.None);   // keyed view read
        Assert.Equal(1, viaKey.TransactionCount);
    }
}
