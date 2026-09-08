namespace SimpleOrm.Tests.AmendFixture
{
    /// <summary>
    /// The amend fixture (ADR-0017 add.3): the model has moved on since the draft
    /// <c>V0002</c> was generated — the draft added <c>note</c>, the model now says
    /// <c>remark</c>. Tests lay the generated files and snapshots on disk
    /// themselves; these compiled types are the assembly-side truth the command
    /// reads (newest version, number of steps).
    /// </summary>
    [Table("amend_widgets")]
    public sealed class AmendWidget
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public required string Name { get; set; }

        [Column]
        public string? Remark { get; set; }
    }

    public sealed class V0001 : MigrationVersion
    {
        public override void Compose(VersionBuilder version) => version.Apply<Table.AmendWidget.V0001_Create>();
    }

    public sealed class V0002 : MigrationVersion
    {
        public override void Compose(VersionBuilder version) => version.Apply<Table.AmendWidget.V0002_AddNote>();
    }
}

namespace SimpleOrm.Tests.AmendFixture.Table.AmendWidget
{
    public sealed class V0001_Create : TableMigration<global::SimpleOrm.Tests.AmendFixture.AmendWidget>
    {
        public override void Action(TableActions actions)
            => actions.Sql("create table amend_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT");
    }

    public sealed class V0002_AddNote : TableMigration<global::SimpleOrm.Tests.AmendFixture.AmendWidget>
    {
        public override void Action(TableActions actions) => actions.AddColumn("note", "TEXT");
    }
}
