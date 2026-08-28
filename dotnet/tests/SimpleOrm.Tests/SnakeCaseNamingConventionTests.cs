using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// These vectors ARE the spec of the default convention (spec/metadata-model.md,
/// milestone 2): every port must produce these database names from its own idiom.
/// </summary>
public sealed class SnakeCaseNamingConventionTests
{
    private readonly SnakeCaseNamingConvention _convention = SnakeCaseNamingConvention.Instance;

    [Theory]
    [InlineData("Name", "name")]
    [InlineData("UserId", "user_id")]
    [InlineData("UserID", "user_id")]
    [InlineData("APIKey", "api_key")]
    [InlineData("HTMLParser", "html_parser")]
    [InlineData("Address2", "address2")]
    [InlineData("Address2B", "address2_b")]
    [InlineData("CreatedAtUtc", "created_at_utc")]
    [InlineData("ID", "id")]
    [InlineData("camelCase", "camel_case")]
    [InlineData("already_snake", "already_snake")]
    public void Column_names_follow_the_pinned_vectors(string property, string expected)
        => Assert.Equal(expected, _convention.ColumnName(property));

    [Theory]
    [InlineData("User", "user")]
    [InlineData("TransactionDetail", "transaction_detail")]
    public void Table_names_are_snake_case_without_pluralization(string type, string expected)
        => Assert.Equal(expected, _convention.TableName(type));

    [Fact]
    public void Index_names_join_table_and_columns()
        => Assert.Equal(
            "ix_transactions_status_created_at",
            _convention.IndexName("transactions", ["status", "created_at"]));
}
