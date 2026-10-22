// Fixture types for TriggerTests (ADR-0042), in their own namespace so the sample models stay untouched:
// every misplaced or malformed declaration the loader must refuse with MAP-029, the scan conflicts, and a builder-linked trigger.
namespace TriggerFixture
{
    using SimpleOrm;

    [Table("narrowed_widgets")]
    [Trigger(typeof(NarrowedTrigger))]
    public sealed class NarrowedWidget
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column("widget_name")]
        public string? Name { get; set; }
    }

    [Trigger("trg_narrowed", TriggerTiming.After, TriggerEvent.Update, "select 1;", UpdateOf = [nameof(NarrowedWidget.Name)])]
    public sealed class NarrowedTrigger;

    [Statement("select 1 as id")]
    [Trigger(typeof(NarrowedTrigger))]
    public sealed class LinkOnStatement
    {
        [Column]
        public long Id { get; set; }
    }

    public sealed class Undeclared;

    [Table("w1")]
    [Trigger(typeof(Undeclared))]
    public sealed class LinksUndeclared
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("t_old_insert", TriggerTiming.After, TriggerEvent.Insert, "select 1 from {old: id} o;")]
    public sealed class OldOnInsert;

    [Table("w2")]
    [Trigger(typeof(OldOnInsert))]
    public sealed class LinksOldOnInsert
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("t_new_delete", TriggerTiming.After, TriggerEvent.Delete, "select 1 from {new: id} n;")]
    public sealed class NewOnDelete;

    [Table("w3")]
    [Trigger(typeof(NewOnDelete))]
    public sealed class LinksNewOnDelete
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("t_unknown", TriggerTiming.After, TriggerEvent.Insert, "select {user};")]
    public sealed class UnknownToken;

    [Table("w4")]
    [Trigger(typeof(UnknownToken))]
    public sealed class LinksUnknownToken
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("t_bare", TriggerTiming.After, TriggerEvent.Insert, "select 1 from {new} n;")]
    public sealed class RowWithoutColumns;

    [Table("w5")]
    [Trigger(typeof(RowWithoutColumns))]
    public sealed class LinksRowWithoutColumns
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("t_of", TriggerTiming.After, TriggerEvent.Insert, "select 1;", UpdateOf = ["Id"])]
    public sealed class UpdateOfWithoutUpdate;

    [Table("w6")]
    [Trigger(typeof(UpdateOfWithoutUpdate))]
    public sealed class LinksUpdateOfWithoutUpdate
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("t_of2", TriggerTiming.After, TriggerEvent.Update, "select 1;", UpdateOf = ["Nope"])]
    public sealed class UpdateOfUnknown;

    [Table("w7")]
    [Trigger(typeof(UpdateOfUnknown))]
    public sealed class LinksUpdateOfUnknown
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("t_col", TriggerTiming.After, TriggerEvent.Update, "select 1 from {new: id, nope} n;")]
    public sealed class UnmappedColumn;

    [Table("w8")]
    [Trigger(typeof(UnmappedColumn))]
    public sealed class LinksUnmappedColumn
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("t_view_after", TriggerTiming.After, TriggerEvent.Update, "select 1;")]
    public sealed class AfterOnView;

    [View("w_view", "select 1 as id")]
    [Trigger(typeof(AfterOnView))]
    public sealed class ViewLinksAfter
    {
        [Column]
        public long Id { get; set; }
    }

    [Table("w9")]
    [Trigger(typeof(NarrowedWidget))]
    public sealed class LinksAnEntity
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger(" ", TriggerTiming.After, TriggerEvent.Update, "select 1;")]
    public sealed class BlankName;

    [Table("w10")]
    [Trigger(typeof(BlankName))]
    public sealed class LinksBlankName
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("t_none", TriggerTiming.After, TriggerEvent.None, "select 1;")]
    public sealed class NoEvents;

    [Table("w11")]
    [Trigger(typeof(NoEvents))]
    public sealed class LinksNoEvents
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Table("w12")]
    [Trigger("t_inline", TriggerTiming.After, TriggerEvent.Update, "select 1;")]
    public sealed class DeclaresInsteadOfLinking
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Trigger("trg_shared", TriggerTiming.After, TriggerEvent.Update, "select 1;")]
    public sealed class SharedTrigger;

    [Trigger("trg_shared", TriggerTiming.After, TriggerEvent.Insert, "select 1;")]
    public sealed class SameNameTrigger;

    [Table("shared_a")]
    [Trigger(typeof(SharedTrigger))]
    public sealed class SharedOwnerA
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Table("shared_b")]
    [Trigger(typeof(SharedTrigger))]
    public sealed class SharedOwnerB
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    [Table("same_name")]
    [Trigger(typeof(SameNameTrigger))]
    public sealed class SameNameOwner
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }

    public sealed class BuiltWidget
    {
        public long Id { get; set; }

        public string? Name { get; set; }
    }

    [Trigger("trg_built", TriggerTiming.After, TriggerEvent.Insert, "select 1 from {new: id} n;")]
    public sealed class BuiltWidgetTrigger;

    [Trigger("t_new_ins_del", TriggerTiming.After, TriggerEvent.Insert | TriggerEvent.Delete, "select 1 from {new: id} n;")]
    public sealed class NewOnInsertAndDelete;

    [Table("w13")]
    [Trigger(typeof(NewOnInsertAndDelete))]
    public sealed class LinksNewOnInsertAndDelete
    {
        [Key]
        [Column]
        public long Id { get; set; }
    }
}
