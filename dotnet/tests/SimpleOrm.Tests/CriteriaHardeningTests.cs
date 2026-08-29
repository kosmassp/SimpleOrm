using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// ADR-0020 add.1 — the adversarial-review fixes: degenerate composites render
/// truth-values, negative paging refuses (QRY-008), the single-string IN trap is
/// closed, null IN lists are named errors, ambiguous property names refuse, IN
/// expansion leaves literals/comments alone, UPDATE never touches generated
/// columns, and a database-generated key must be an integer.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class CriteriaHardeningTests(SqliteFixture fixture)
{
    [Fact]
    public async Task Empty_composites_run_as_truth_values()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "EmptyComposite", "empty-composite@example.com");

        // Empty AND is true — the row comes back; empty OR is false — none do.
        var all = await db.Query<User>()
            .Where(Criteria.And(), Criteria.Like(nameof(User.Email), "empty-composite@%"))
            .ToListAsync(CancellationToken.None);
        Assert.Single(all);

        var none = await db.Query<User>()
            .Where(Criteria.Or(), Criteria.Like(nameof(User.Email), "empty-composite@%"))
            .ToListAsync(CancellationToken.None);
        Assert.Empty(none);
    }

    [Fact]
    public async Task Negative_paging_is_refused_not_silently_unlimited()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var refused = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.Query<User>().Limit(-1).ToListAsync(CancellationToken.None));
        Assert.Equal("QRY-008", refused.Code);
        Assert.Equal("QRY-008", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.Query<User>().Offset(-3).ToListAsync(CancellationToken.None))).Code);
    }

    [Fact]
    public async Task Single_string_In_means_one_value_not_characters()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "InTrap", "in-trap@example.com");

        // Before the overload guard this resolved to In<char> and queried per character.
        var rows = await db.Query<User>()
            .Where(Criteria.In(nameof(User.Name), "InTrap"))
            .ToListAsync(CancellationToken.None);
        Assert.Equal(ada.Id, Assert.Single(rows).Id);
    }

    [Fact]
    public void Null_In_lists_are_named_errors_at_the_factory()
    {
        var byArray = Assert.Throws<ArgumentNullException>(() => Criteria.In("Id", (object?[])null!));
        Assert.Contains("'Id'", byArray.Message);
        Assert.Throws<ArgumentNullException>(() => Criteria.In<int>("Id", null!));
    }

    [Fact]
    public void Ambiguous_case_insensitive_property_refuses_instead_of_first_wins()
    {
        var map = new EntityMapLoader().Load<CaseCollision>();
        var exact = AnsiSelectRenderer.Resolve(map, "ID", "test");
        Assert.Equal("ID", exact.PropertyName);                       // exact case wins outright

        var ambiguous = Assert.Throws<SimpleOrmException>(() => AnsiSelectRenderer.Resolve(map, "iD", "test"));
        Assert.Equal("QRY-006", ambiguous.Code);
        Assert.Contains("ambiguous", ambiguous.Message);
    }

    [Fact]
    public async Task In_expansion_leaves_literals_and_comments_alone()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "ExpandLit", "expand-lit@ids");

        // '@ids' appears inside a string literal and a comment: only the real
        // placeholder expands (previously the literal was rewritten too).
        Query<IdsArgs, User> query = Query.Inline(
            "select id, name, email, display_name, created_at, updated_at from users " +
            "where email like '%@ids' -- narrows by @ids\n and id in (@ids)");
        var rows = await db.QueryAsync(query, new IdsArgs([ada.Id]), CancellationToken.None);
        Assert.Equal(ada.Id, Assert.Single(rows).Id);
    }

    public sealed record IdsArgs(IReadOnlyList<long> Ids);

    [Fact]
    public void Update_never_touches_generated_non_key_columns()
    {
        var map = new EntityMapLoader().Load<StampedRow>();
        var sql = new SqliteDialect().UpdateSql(map);
        Assert.DoesNotContain("stamp = @stamp", sql);
        Assert.Contains("label = @label", sql);
    }

    [Fact]
    public void Database_generated_key_must_be_an_integer()
    {
        var exception = Assert.Throws<MappingException>(() => new EntityMapLoader().Load<GuidGenerated>());
        Assert.Contains(exception.Errors, e => e.Code == "MAP-019" && e.Message.Contains("integer"));
    }

    // --- fixtures -------------------------------------------------------------------

    [Table("case_collisions")]
    private sealed class CaseCollision
    {
        [Key]
        [Generated]
        [Column("id_upper")]
        public long ID { get; set; }

        [Column("id_lower")]
        public long Id { get; set; }
    }

    [Table("stamped_rows")]
    private sealed class StampedRow
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? Label { get; set; }

        /// <summary>Database-owned (e.g. a trigger-maintained timestamp): read, never written.</summary>
        [Generated]
        [Column]
        public string? Stamp { get; set; }
    }

    [Table("guid_generated_rows")]
    private sealed class GuidGenerated
    {
        [Key]
        [Generated]
        [Column]
        public Guid Id { get; set; }
    }
}
