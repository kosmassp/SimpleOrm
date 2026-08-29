using GenModels;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// ADR-0017: the diff generator core. Metadata is the final truth; the latest
/// snapshot is the past; the diff is the migration. Renames are declared, never
/// inferred; destructive and inexpressible changes gate on DDL-003/DDL-004.
/// </summary>
public sealed class GeneratorTests
{
    private static readonly SqliteDialect Dialect = new();
    private static readonly EntityMap Map = new EntityMapLoader().Load<Widget>();

    private static TableSchema Snapshot(
        IReadOnlyList<TableSchema.Column> columns, IReadOnlyList<TableSchema.Index>? indexes = null)
        => new("gen_widgets", columns, indexes ?? [CurrentIndex()]);

    private static TableSchema.Column Id() => new("id", "INTEGER", nullable: false, key: true, generated: true);

    private static TableSchema.Column NameCol() => new("name", "TEXT", nullable: false);

    private static TableSchema.Column NoteCol() => new("note", "TEXT", nullable: true);

    private static TableSchema.Index CurrentIndex()
        => new("ix_gen_widgets_name", [new TableSchema.Index.Part("name")]);

    private static readonly Dictionary<string, string> NoRenames = [];

    [Fact]
    public void Missing_snapshot_means_new_table()
    {
        var diff = MigrationGenerator.Diff(Map, Dialect, snapshot: null, NoRenames);
        Assert.True(diff.IsNew);
        Assert.True(diff.HasChanges);
    }

    [Fact]
    public void Identical_snapshot_means_no_changes()
    {
        var diff = MigrationGenerator.Diff(Map, Dialect, Snapshot([Id(), NameCol(), NoteCol()]), NoRenames);
        Assert.False(diff.HasChanges);
        Assert.Empty(diff.Unsupported);
    }

    [Fact]
    public void Nullable_addition_is_generated_and_non_nullable_is_not()
    {
        var addNote = MigrationGenerator.Diff(Map, Dialect, Snapshot([Id(), NameCol()]), NoRenames);
        var added = Assert.Single(addNote.Added);
        Assert.Equal("note", added.Name);

        // Adding NOT NULL "name" needs a default/backfill: DDL-004, write it by hand.
        var addName = MigrationGenerator.Diff(Map, Dialect, Snapshot([Id(), NoteCol()]), NoRenames);
        Assert.Contains(addName.Unsupported, m => m.Contains("name"));
        Assert.Empty(addName.Added);
    }

    [Fact]
    public void Removed_column_and_type_change_are_detected()
    {
        var removed = MigrationGenerator.Diff(
            Map, Dialect, Snapshot([Id(), NameCol(), NoteCol(), new TableSchema.Column("legacy", "TEXT", nullable: true)]), NoRenames);
        Assert.Equal("legacy", Assert.Single(removed.Removed).Name);

        var retyped = MigrationGenerator.Diff(
            Map, Dialect, Snapshot([Id(), NameCol(), new TableSchema.Column("note", "INTEGER", nullable: true)]), NoRenames);
        Assert.Contains(retyped.Unsupported, m => m.Contains("note") && m.Contains("INTEGER"));
    }

    [Fact]
    public void Declared_rename_is_a_rename_not_add_plus_remove()
    {
        var snapshot = Snapshot([Id(), NameCol(), new TableSchema.Column("remark", "TEXT", nullable: true)]);
        var diff = MigrationGenerator.Diff(Map, Dialect, snapshot, new Dictionary<string, string> { ["remark"] = "note" });

        Assert.Equal(("remark", "note"), Assert.Single(diff.Renamed));
        Assert.Empty(diff.Added);
        Assert.Empty(diff.Removed);

        // Without the declaration the same shapes read as add + remove (never inferred).
        var undeclared = MigrationGenerator.Diff(Map, Dialect, snapshot, NoRenames);
        Assert.Equal("note", Assert.Single(undeclared.Added).Name);
        Assert.Equal("remark", Assert.Single(undeclared.Removed).Name);
    }

    [Fact]
    public void Index_additions_and_removals_are_diffed_by_name()
    {
        var noIndex = MigrationGenerator.Diff(Map, Dialect, Snapshot([Id(), NameCol(), NoteCol()], indexes: []), NoRenames);
        Assert.Contains("ix_gen_widgets_name", Assert.Single(noIndex.AddedIndexSql));

        var staleIndex = MigrationGenerator.Diff(
            Map, Dialect,
            Snapshot([Id(), NameCol(), NoteCol()], [CurrentIndex(), new TableSchema.Index("ix_old", [new TableSchema.Index.Part("note")])]),
            NoRenames);
        Assert.Equal("ix_old", Assert.Single(staleIndex.RemovedIndexNames));
    }

    [Fact]
    public void Emitted_new_table_step_creates_up_and_drops_down()
    {
        var diff = MigrationGenerator.Diff(Map, Dialect, snapshot: null, NoRenames);
        var code = MigrationGenerator.EmitTableStep(
            "My.Migrations", typeof(Widget), Map, Dialect, version: 3, "CreateWidgets", diff);

        Assert.Contains("namespace My.Migrations.Table.Widget;", code);
        Assert.Contains("class V0003_CreateWidgets : TableMigration<global::GenModels.Widget>", code);
        Assert.Contains("create table if not exists gen_widgets", code);
        Assert.Contains("ix_gen_widgets_name", code);
        Assert.Contains("actions.DropTable();", code);
    }

    [Fact]
    public void Emitted_change_step_has_literal_actions_and_derived_down()
    {
        var snapshot = Snapshot([Id(), NameCol(), new TableSchema.Column("remark", "TEXT", nullable: true), new TableSchema.Column("legacy", "TEXT", nullable: true)]);
        var diff = MigrationGenerator.Diff(Map, Dialect, snapshot, new Dictionary<string, string> { ["remark"] = "note" });
        var code = MigrationGenerator.EmitTableStep(
            "My.Migrations", typeof(Widget), Map, Dialect, version: 4, "Reshape", diff);

        Assert.Contains("actions.RenameColumn(\"remark\", \"note\");", code);
        Assert.Contains("actions.RemoveColumn(\"legacy\");", code);

        // The Down derives from the snapshot: reverse rename, restore the removed column.
        Assert.Contains("actions.RenameColumn(\"note\", \"remark\");", code);
        Assert.Contains("actions.AddColumn(\"legacy\", \"TEXT\");", code);
    }

    [Fact]
    public void Emitted_view_create_is_literal_with_a_guarded_drop_down()
    {
        var code = MigrationGenerator.EmitViewStep(
            "My.Migrations", typeof(Widget), "View", "widget_totals",
            version: 5, "CreateTotals", "create view widget_totals as select 1 as one", previousDdl: null);

        Assert.Contains("namespace My.Migrations.View.Widget;", code);
        Assert.Contains("actions.Sql(\"create view widget_totals as select 1 as one\");", code);
        Assert.Contains("actions.ExpectDefinition(\"create view widget_totals as select 1 as one\");", code);
        Assert.Contains("actions.Sql(\"drop view if exists widget_totals\");", code);
    }

    [Fact]
    public void Emitted_view_change_guards_the_previous_definition_and_derives_the_down()
    {
        var code = MigrationGenerator.EmitViewStep(
            "My.Migrations", typeof(Widget), "View", "widget_totals",
            version: 6, "AddTwo",
            "create view widget_totals as select 1 as one, 2 as two",
            "create view widget_totals as select 1 as one");

        // Up: expect the previous definition (MIG-012 on outside drift), drop, create the new one.
        Assert.Contains("actions.ExpectDefinition(\"create view widget_totals as select 1 as one\");", code);
        Assert.Contains("actions.Sql(\"create view widget_totals as select 1 as one, 2 as two\");", code);

        // Down: expect the new definition, then restore the previous one from the snapshot.
        Assert.Contains("actions.ExpectDefinition(\"create view widget_totals as select 1 as one, 2 as two\");", code);
        Assert.Contains("actions.Sql(\"create view widget_totals as select 1 as one\");", code);
    }

    [Fact]
    public void Emitted_root_composes_steps_in_order()
    {
        var code = MigrationGenerator.EmitRoot("My.Migrations", 4, ["Table.Widget.V0004_Reshape", "Table.Other.V0004_Reshape"]);
        Assert.Contains("class V0004 : MigrationVersion", code);
        Assert.Contains(".Apply<Table.Widget.V0004_Reshape>()", code);
        Assert.Contains(".Apply<Table.Other.V0004_Reshape>();", code);
    }
}
