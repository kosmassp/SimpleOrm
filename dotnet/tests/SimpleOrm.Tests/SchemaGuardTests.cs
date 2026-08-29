using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>Milestone 6: SchemaGuard — every rule has a failing fixture and the report names the source.</summary>
[Collection(SqliteCollection.Name)]
public sealed class SchemaGuardTests(SqliteFixture fixture)
{
    [Fact]
    public async Task The_sample_validates_clean()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await SchemaGuard.ValidateAsync(db, typeof(User).Assembly, CancellationToken.None);   // no throw
    }

    [Fact]
    public async Task Missing_migrations_are_MIG030()
    {
        var path = Path.Combine(Path.GetTempPath(), $"simpleorm_guard_{Guid.NewGuid():N}.db");
        try
        {
            await using var db = await Db.OpenAsync(
                $"Data Source={path}", TestDb.Options, CancellationToken.None);
            var exception = await Assert.ThrowsAsync<SchemaValidationException>(
                () => SchemaGuard.ValidateAsync(db, typeof(User).Assembly, CancellationToken.None));

            Assert.Contains(exception.Errors, e => e.Code == "MIG-030");
            Assert.Contains(exception.Errors, e => e.Code == "VAL-012");    // entities also missing
        }
        finally
        {
            Microsoft.Data.Sqlite.SqliteConnection.ClearAllPools();
            File.Delete(path);
        }
    }

    // --- broken registry fixtures ---------------------------------------------------

    public static class BadRegistry
    {
        public static readonly Query<EmptyArgs, long> BadSql =
            Query.Inline("select frm users");

        public static readonly Query<EmptyArgs, User> Star =
            Query.Inline("select * from users");

        public static readonly Query<EmptyArgs, long> CountStarIsFine =
            Query.Inline("select count(*) as n from users -- notnull: n");

        public static readonly Query<ProbeArgs, long> WrongParams =
            Query.Inline("select count(id) from users where name = @Nope");

        public static readonly Query<EmptyArgs, WrongShapeRow> WrongShape =
            Query.Inline("select id, name, 1 as mystery from users");

        public static readonly Query<EmptyArgs, StampRow> NullableIntoNonNullable =
            Query.Inline("select updated_at as stamp from users");

        public static readonly Query<EmptyArgs, TotalRow> ExpressionNeedsNullable =
            Query.Inline("select 1 + 1 as total from users");

        public static readonly Query<EmptyArgs, TotalRow> ExpressionWithComment =
            Query.Inline("select 1 + 1 as total from users -- notnull: total");

        public static readonly Query<EmptyArgs, NameAsLongRow> DeclaredTypeMismatch =
            Query.Inline("select name from users");

        public static readonly Command<EmptyArgs> NowLint =
            Query.Inline("update users set updated_at = current_timestamp");

        public sealed record ProbeArgs(long Id);

        public sealed record WrongShapeRow(long Id, string Name);

        public sealed record StampRow(DateTime Stamp);

        public sealed record TotalRow(long Total);

        public sealed record NameAsLongRow(long Name);
    }

    [Table("nope_table")]
    public sealed class MissingRelationEntity
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Table("users")]
    public sealed class GhostColumnEntity
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? GhostColumn { get; set; }
    }

    [Fact]
    public async Task Every_rule_fires_with_its_code_in_one_report()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var exception = await Assert.ThrowsAsync<SchemaValidationException>(
            () => SchemaGuard.ValidateTypesAsync(
                db,
                [typeof(BadRegistry), typeof(MissingRelationEntity), typeof(GhostColumnEntity)],
                CancellationToken.None));
        var errors = exception.Errors;

        Assert.Contains(errors, e => e.Code == "VAL-001" && e.Source == "BadRegistry.BadSql");
        Assert.Contains(errors, e => e.Code == "VAL-021" && e.Source == "BadRegistry.Star");
        Assert.DoesNotContain(errors, e => e.Source == "BadRegistry.CountStarIsFine");
        Assert.Contains(errors, e => e.Code == "PRM-001" && e.Source == "BadRegistry.WrongParams");
        Assert.Contains(errors, e => e.Code == "PRM-002" && e.Source == "BadRegistry.WrongParams");
        Assert.Contains(errors, e => e.Code == "MAP-001" && e.Source == "BadRegistry.WrongShape");
        Assert.Contains(errors, e => e.Code == "VAL-010" && e.Source == "BadRegistry.NullableIntoNonNullable");
        Assert.Contains(errors, e => e.Code == "VAL-010" && e.Source == "BadRegistry.ExpressionNeedsNullable");
        Assert.DoesNotContain(errors, e => e.Source == "BadRegistry.ExpressionWithComment");
        Assert.Contains(errors, e => e.Code == "VAL-011" && e.Source == "BadRegistry.DeclaredTypeMismatch");
        Assert.Contains(errors, e => e.Code == "VAL-020" && e.Source == "BadRegistry.NowLint");
        Assert.Contains(errors, e => e.Code == "VAL-012" && e.Source == nameof(MissingRelationEntity));
        Assert.Contains(errors, e => e.Code == "VAL-013" && e.Source == nameof(GhostColumnEntity));

        // The report is complete and grouped, not first-error-only.
        Assert.True(errors.Count >= 11);
        Assert.Contains("BadRegistry.WrongParams", exception.Message);
    }

    [Fact]
    public async Task Validation_leaves_no_trace()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        // NowLint is an UPDATE; validation must prepare it without executing it.
        try
        {
            await SchemaGuard.ValidateTypesAsync(db, [typeof(BadRegistry)], CancellationToken.None);
        }
        catch (SchemaValidationException)
        {
        }

        var ada = await db.Query<User>()
            .Where(Criteria.Eq(nameof(User.Email), "ada@example.com"))
            .SingleAsync(CancellationToken.None);
        Assert.Null(ada.UpdatedAtUtc);
    }
}
