// Fixture entities for GeneratorTests and SchemaSyncTests, in their own namespace
// so the sample models stay untouched. Each sync test owns a distinct table name:
// the SQLite test databases are shared per collection.
namespace GenModels
{
    [SimpleOrm.Table("gen_widgets")]
    [SimpleOrm.Index(nameof(Name))]
    public sealed class Widget
    {
        [SimpleOrm.Key]
        [SimpleOrm.Generated]
        [SimpleOrm.Column]
        public long Id { get; set; }

        [SimpleOrm.Column]
        public string Name { get; set; } = string.Empty;

        [SimpleOrm.Column]
        public string? Note { get; set; }
    }

    [SimpleOrm.Table("sync_new_widgets")]
    public sealed class SyncNew
    {
        [SimpleOrm.Key]
        [SimpleOrm.Generated]
        [SimpleOrm.Column]
        public long Id { get; set; }

        [SimpleOrm.Column]
        public string? Label { get; set; }
    }

    [SimpleOrm.Table("sync_add_widgets")]
    public sealed class SyncAdd
    {
        [SimpleOrm.Key]
        [SimpleOrm.Generated]
        [SimpleOrm.Column]
        public long Id { get; set; }

        [SimpleOrm.Column]
        public string Name { get; set; } = string.Empty;

        [SimpleOrm.Column]
        public string? Note { get; set; }
    }

    [SimpleOrm.Table("sync_bad_widgets")]
    public sealed class SyncBad
    {
        [SimpleOrm.Key]
        [SimpleOrm.Generated]
        [SimpleOrm.Column]
        public long Id { get; set; }

        [SimpleOrm.Column]
        public string? Note { get; set; }
    }
}
