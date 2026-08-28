using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>Happy-path metadata loading across the nine sample models.</summary>
public sealed class EntityMapLoaderTests
{
    private readonly EntityMapLoader _loader = new();

    [Fact]
    public void User_maps_table_generated_key_and_inherited_audit_columns()
    {
        var map = _loader.Load<User>();

        Assert.Equal(RelationKind.Table, map.Kind);
        Assert.Equal("users", map.RelationName);
        Assert.Equal(KeyStrategy.DatabaseGenerated, map.KeyStrategy);
        Assert.Equal(["id"], map.KeyProperties.Select(k => k.ColumnName));

        // Derived class columns first, BaseModel audit columns last, [Column] overrides applied.
        Assert.Equal(["id", "name", "email", "created_at", "updated_at"], map.Properties.Select(p => p.ColumnName));
        Assert.False(map.Properties.Single(p => p.ColumnName == "name").IsNullable);
        Assert.True(map.Properties.Single(p => p.ColumnName == "updated_at").IsNullable);

        var index = Assert.Single(map.Indexes);
        Assert.Equal("ix_users_email", index.Name);
        Assert.True(index.Unique);
    }

    [Fact]
    public void UserRole_maps_composite_natural_key_and_two_relationships()
    {
        var map = _loader.Load<UserRole>();

        Assert.Equal(KeyStrategy.Natural, map.KeyStrategy);
        Assert.Equal(["user_id", "role_id"], map.KeyProperties.Select(k => k.ColumnName));
        Assert.Equal(2, map.Relationships.Count);
        Assert.Equal(typeof(User), map.Relationships.Single(r => r.PropertyName == "User").TargetType);
        Assert.Equal("RoleId", map.Relationships.Single(r => r.PropertyName == "Role").ForeignKeyProperty);
    }

    [Fact]
    public void Transaction_maps_version_indexes_and_foreign_key()
    {
        var map = _loader.Load<Transaction>();

        Assert.Equal("version", map.VersionProperty?.ColumnName);
        Assert.Equal(typeof(User), map.Properties.Single(p => p.ColumnName == "user_id").ForeignKeyReferences);

        Assert.Equal(2, map.Indexes.Count);
        Assert.Equal("ix_transactions_user_id", map.Indexes[0].Name);
        var named = map.Indexes[1];
        Assert.Equal("ix_transactions_status_created", named.Name);
        Assert.Equal([false, true], named.Columns.Select(c => c.Descending));

        var relationship = Assert.Single(map.Relationships);
        Assert.Equal("UserId", relationship.ForeignKeyProperty);
    }

    [Fact]
    public void View_and_materialized_view_map_with_their_capabilities()
    {
        var view = _loader.Load<UserTransactionTotal>();
        Assert.Equal(RelationKind.View, view.Kind);
        Assert.Equal(KeyStrategy.Natural, view.KeyStrategy);
        Assert.Empty(view.Indexes);

        var materialized = _loader.Load<MonthlySalesTotal>();
        Assert.Equal(RelationKind.MaterializedView, materialized.Kind);
        Assert.True(Assert.Single(materialized.Indexes).Unique);
    }

    [Fact]
    public void Statement_maps_sql_and_declared_parameters()
    {
        var map = _loader.Load<DailySales>();

        Assert.Equal(RelationKind.Statement, map.Kind);
        Assert.Null(map.RelationName);
        Assert.Contains("@since", map.DefiningSql);
        Assert.Equal(KeyStrategy.None, map.KeyStrategy);

        var parameter = Assert.Single(map.StatementParameters);
        Assert.Equal("since", parameter.Name);
        Assert.Equal(typeof(DateTime), parameter.ClrType);
    }

    [Fact]
    public void Procedure_maps_keyless_and_nullable_columns()
    {
        var map = _loader.Load<UserActivityReport>();

        Assert.Equal(RelationKind.Procedure, map.Kind);
        Assert.Equal("user_activity_report", map.RelationName);
        Assert.Equal(KeyStrategy.None, map.KeyStrategy);
        Assert.Contains("@since", map.DefiningSql);
        Assert.Equal("since", Assert.Single(map.StatementParameters).Name);
        Assert.True(map.Properties.Single(p => p.ColumnName == "last_transaction_at_utc").IsNullable);
    }

    [Fact]
    public void Maps_are_cached_per_loader()
        => Assert.Same(_loader.Load<User>(), _loader.Load<User>());
}
