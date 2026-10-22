using Microsoft.Data.Sqlite;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using SimpleOrm.Postgres;
using TriggerFixture;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Triggers in the mapping (Level 4, ADR-0042): a trigger is a class carrying one
/// neutral body, linked from its entity; the dialect renders header, wrapper,
/// and tokens; migrations own it as an object kind with snapshots, the
/// <c>MIG-012</c> guard, and a derived rollback.
/// </summary>
public sealed class TriggerTests : IDisposable
{
    private readonly string _path = Path.Combine(Path.GetTempPath(), $"simpleorm_trg_{Guid.NewGuid():N}.db");

    private Task<Db> OpenAsync()
        => Db.OpenAsync($"Data Source={_path}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);

    public void Dispose()
    {
        SqliteConnection.ClearAllPools();
        File.Delete(_path);
    }

    private static MappingException LoadFails(Type type)
        => Assert.Throws<MappingException>(() => new EntityMapLoader().Load(type));

    // --- declaration -----------------------------------------------------------------

    [Fact]
    public void The_entity_links_the_class_and_the_map_carries_the_declaration()
    {
        var map = new EntityMapLoader().Load<Role>();
        var trigger = Assert.Single(map.Triggers);
        Assert.Equal(typeof(RoleAuditTrigger), trigger.TriggerType);
        Assert.Equal("trg_roles_audit", trigger.Name);
        Assert.Equal(TriggerTiming.After, trigger.Definition.Timing);
        Assert.Equal(TriggerEvent.Update, trigger.Definition.Events);
        Assert.Empty(trigger.Definition.UpdateOf);
        Assert.Contains("{new: id, role_name}", trigger.Definition.Sql);
        Assert.Equal("trg_roles_audit", EntityMapLoader.TriggerNameOf(typeof(RoleAuditTrigger)));
    }

    [Fact]
    public void The_export_carries_triggers_only_when_declared()
    {
        var loader = new EntityMapLoader();
        var role = EntityMapJson.Export(loader.Load<Role>(), loader);
        Assert.Contains("\"triggers\": [", role);
        Assert.Contains("\"name\": \"trg_roles_audit\"", role);
        Assert.Contains("\"timing\": \"after\"", role);
        Assert.Contains("\"sql\": \"insert into role_changes (role_id, role_name, changed_at) select n.id, n.role_name, {now} from {new: id, role_name} n;\"", role);
        Assert.DoesNotContain("RoleAuditTrigger", role);   // never a language-side name
        Assert.DoesNotContain("\"triggers\"", EntityMapJson.Export(loader.Load<User>(), loader));
    }

    [Fact]
    public void Update_of_resolves_properties_to_columns_in_the_export()
    {
        var map = new EntityMapLoader().Load<NarrowedWidget>();
        var trigger = Assert.Single(map.Triggers);
        Assert.Equal(["widget_name"], trigger.Definition.UpdateOf);
    }

    [Theory]
    [InlineData(typeof(LinkOnStatement))]
    [InlineData(typeof(LinksUndeclared))]
    [InlineData(typeof(LinksOldOnInsert))]
    [InlineData(typeof(LinksNewOnDelete))]
    [InlineData(typeof(LinksUnknownToken))]
    [InlineData(typeof(LinksRowWithoutColumns))]
    [InlineData(typeof(LinksUpdateOfWithoutUpdate))]
    [InlineData(typeof(LinksUpdateOfUnknown))]
    [InlineData(typeof(LinksUnmappedColumn))]
    [InlineData(typeof(ViewLinksAfter))]
    [InlineData(typeof(LinksAnEntity))]
    [InlineData(typeof(LinksBlankName))]
    [InlineData(typeof(LinksNoEvents))]
    [InlineData(typeof(DeclaresInsteadOfLinking))]
    public void Every_misplaced_or_malformed_declaration_is_MAP029(Type entity)
    {
        var exception = LoadFails(entity);
        Assert.All(exception.Errors, e => Assert.Equal("MAP-029", e.Code));
    }

    [Fact]
    public void Tokens_canonicalize_and_literals_and_comments_are_handled()
    {
        // Any spelling of a token normalizes to the canonical one; braces inside a string literal are text.
        var definition = new TriggerDefinition("t", TriggerTiming.After, TriggerEvent.Update, "select  '{x}', n.id from { NEW : id ,name } n;");
        Assert.Equal("select '{x}', n.id from {new: id, name} n;", definition.NormalizedSql);
        var errors = new List<MappingError>();
        Assert.Equal(2, TriggerTokens.Parse("select '{x}' from {new: id} n; -- note", "t", errors).Count(s => s.Literal is not null) + 0);
        Assert.Contains(errors, e => e.Code == "MAP-029" && e.Message.Contains("line comment"));
        var clean = new List<MappingError>();
        TriggerTokens.Parse("select '--' from {new: id} n; /* ok */", "t", clean);   // a -- inside a literal is text
        Assert.Empty(clean);
    }

    [Fact]
    public void A_row_token_must_have_rows_on_every_declared_event()
    {
        // (insert, delete) shares one body: {new} has no rows on delete, {old} none on insert.
        var exception = LoadFails(typeof(LinksNewOnInsertAndDelete));
        Assert.Contains(exception.Errors, e => e.Code == "MAP-029" && e.Message.Contains("delete event"));
    }

    [Fact]
    public void Sql_server_lets_a_delete_through_the_update_of_prologue()
    {
        var narrowed = new TriggerDefinition("t", TriggerTiming.After, TriggerEvent.Update | TriggerEvent.Delete, "select 1;", ["a"]);
        var sql = Assert.Single(new SqlServerDialect().CreateTriggerSql("w", false, narrowed));
        Assert.Contains("if exists (select 1 from inserted) and not (update([a])) return;", sql);

        var updateOnly = new TriggerDefinition("t", TriggerTiming.After, TriggerEvent.Update, "select 1;", ["a"]);
        Assert.Contains("    if not (update([a])) return;", Assert.Single(new SqlServerDialect().CreateTriggerSql("w", false, updateOnly)));
    }

    [Fact]
    public void A_trigger_class_is_not_an_entity()
    {
        Assert.Equal("MAP-029", Assert.Single(LoadFails(typeof(RoleAuditTrigger)).Errors).Code);
        Assert.True(EntityMapLoader.IsTriggerType(typeof(RoleAuditTrigger)));
        Assert.DoesNotContain(typeof(RoleAuditTrigger), EntityMapLoader.ScannedEntityTypes(typeof(Role).Assembly.GetExportedTypes()));
    }

    [Fact]
    public void The_scan_refuses_a_class_linked_twice_and_a_name_declared_twice()
    {
        var conflicts = EntityMapLoader.TriggerConflicts([typeof(SharedOwnerA), typeof(SharedOwnerB), typeof(SameNameOwner)]);
        Assert.Equal(2, conflicts.Count);
        Assert.All(conflicts, e => Assert.Equal("MAP-029", e.Code));
        Assert.Contains(conflicts, e => e.Target == nameof(SharedTrigger) && e.Message.Contains("SharedOwnerA") && e.Message.Contains("SharedOwnerB"));
        Assert.Contains(conflicts, e => e.Target == "trg_shared" && e.Message.Contains(nameof(SameNameTrigger)));

        var exception = Assert.Throws<MappingException>(
            () => EntityMapLoader.ScannedEntityTypes([typeof(SharedOwnerA), typeof(SharedOwnerB)]));
        Assert.Equal("MAP-029", exception.Errors[0].Code);
    }

    [Fact]
    public void The_manual_builder_links_a_trigger_under_the_same_rules()
    {
        var builder = new EntityMapBuilder<BuiltWidget>().ToTable("built_widgets").Trigger<BuiltWidgetTrigger>();
        builder.Property(w => w.Id).Key().Generated();
        builder.Property(w => w.Name);
        var map = new EntityMapLoader(new MappingOptions().Register(builder)).Load<BuiltWidget>();
        Assert.Equal("trg_built", Assert.Single(map.Triggers).Name);

        var bad = new EntityMapBuilder<BuiltWidget>().ToTable("built_widgets").Trigger<RoleAuditTrigger>();
        bad.Property(w => w.Id).Key().Generated();
        var options = new MappingOptions().Register(bad);
        Assert.Equal("MAP-029", Assert.Single(Assert.Throws<MappingException>(() => new EntityMapLoader(options).Load<BuiltWidget>()).Errors).Code);
    }

    // --- rendering -------------------------------------------------------------------

    [Fact]
    public void Dialects_refuse_what_they_have_no_form_for_with_DDL005()
    {
        var before = new TriggerDefinition("t", TriggerTiming.Before, TriggerEvent.Insert, "select 1;");
        var instead = new TriggerDefinition("t", TriggerTiming.InsteadOf, TriggerEvent.Insert, "select 1;");

        Assert.Equal("DDL-005", Assert.Throws<SimpleOrmException>(() => new SqlServerDialect().CreateTriggerSql("w", false, before)).Code);
        Assert.Equal("DDL-005", Assert.Throws<SimpleOrmException>(() => new SqliteDialect().CreateTriggerSql("w", false, instead)).Code);
        Assert.Equal("DDL-005", Assert.Throws<SimpleOrmException>(() => new PostgresDialect().CreateTriggerSql("w", false, instead)).Code);

        Assert.Single(new SqliteDialect().CreateTriggerSql("v", true, instead));      // on a view everywhere
        Assert.Single(new SqlServerDialect().CreateTriggerSql("w", false, instead));   // SQL Server: tables too
        Assert.Equal(2, new PostgresDialect().CreateTriggerSql("w", false, before).Count);
    }

    [Fact]
    public void Sqlite_renders_a_multi_event_declaration_as_one_trigger_per_event()
    {
        var definition = new TriggerDefinition("trg_w", TriggerTiming.After, TriggerEvent.Update | TriggerEvent.Delete, "select 1;");
        var created = new SqliteDialect().CreateTriggerSql("widgets", false, definition);
        Assert.Equal(2, created.Count);
        Assert.StartsWith("create trigger trg_w_update after update on widgets for each row", created[0]);
        Assert.StartsWith("create trigger trg_w_delete after delete on widgets for each row", created[1]);
        Assert.Equal(4, new SqliteDialect().DropTriggerSql("widgets", "trg_w").Count);
    }

    [Fact]
    public void The_sqlite_parser_inverts_the_rendering()
    {
        var map = new EntityMapLoader().Load<Role>();
        var definition = map.Triggers[0].Definition;
        var rendered = Assert.Single(new SqliteDialect().CreateTriggerSql("roles", false, definition));
        var parsed = SqliteTriggerDdl.Parse(rendered);
        Assert.NotNull(parsed);
        Assert.Equal(("roles", TriggerTiming.After, TriggerEvent.Update), (parsed.Value.Relation, parsed.Value.Timing, parsed.Value.Event));
        Assert.Equal(definition.NormalizedSql, new TriggerDefinition("x", TriggerTiming.After, TriggerEvent.Update, parsed.Value.Body).NormalizedSql);
    }

    [Fact]
    public void The_generator_emits_the_literal_neutral_declaration()
    {
        var current = new TriggerDefinition("trg_roles_audit", TriggerTiming.After, TriggerEvent.Update | TriggerEvent.Delete, "select 1 from {old: id} o;", ["role_name"]);
        var previous = new TriggerDefinition("trg_roles_audit", TriggerTiming.After, TriggerEvent.Update, "select 1;");
        var code = MigrationGenerator.EmitTriggerStep("My.Migrations", typeof(Role), typeof(RoleAuditTrigger), 14, "Change", current, previous);

        Assert.Contains("namespace My.Migrations.Trigger.RoleAuditTrigger;", code);
        Assert.Contains("TriggerMigration<global::SimpleOrm.Sample.Models.Role, global::SimpleOrm.Sample.Models.RoleAuditTrigger>", code);
        Assert.Contains("actions.ExpectDefinition(TriggerTiming.After, TriggerEvent.Update,", code);
        Assert.Contains("actions.Recreate(TriggerTiming.After, TriggerEvent.Update | TriggerEvent.Delete,", code);
        Assert.Contains("select 1 from {old: id} o;", code);
        Assert.Contains("\"\"\", \"role_name\");", code);
        Assert.DoesNotContain("Down", code);

        var removal = MigrationGenerator.EmitTriggerStep("My.Migrations", typeof(Role), typeof(RoleAuditTrigger), 14, "Drop", null, previous);
        Assert.Contains("actions.DropTrigger();", removal);
    }

    [Fact]
    public void A_layout_only_change_of_the_body_declares_nothing()
    {
        var a = new TriggerSnapshot("roles", false, new TriggerDefinition("t", TriggerTiming.After, TriggerEvent.Update, "select  1\n  from x;"));
        var b = new TriggerSnapshot("roles", false, new TriggerDefinition("t", TriggerTiming.After, TriggerEvent.Update, "select 1 from x;"));
        Assert.False(MigrationGenerator.DiffTrigger(a, b).HasChanges);
        Assert.True(MigrationGenerator.DiffTrigger(a, null).IsNew);
        Assert.True(MigrationGenerator.DiffTrigger(null, b).IsRemoved);
        Assert.True(MigrationGenerator.DiffTrigger(new TriggerSnapshot("users", false, a.Definition), b).IsChanged);
    }

    // --- the database ----------------------------------------------------------------

    [Fact]
    public async Task CreateTriggersAsync_creates_the_declared_triggers_and_they_fire()
    {
        await using var db = await OpenAsync();
        await db.CreateTableAsync<Role>(CancellationToken.None);
        await db.CreateTableAsync<RoleChange>(CancellationToken.None);
        await db.CreateTriggersAsync<Role>(CancellationToken.None);
        await db.CreateTriggersAsync<Role>(CancellationToken.None);   // replace semantics: idempotent

        var role = new Role { Name = "editor", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(role, CancellationToken.None);
        Assert.Empty(await db.QueryAllAsync<RoleChange>(CancellationToken.None));   // an insert does not fire an update trigger

        role.Name = "author";
        await db.UpdateAsync(role, CancellationToken.None);
        var change = Assert.Single(await db.QueryAllAsync<RoleChange>(CancellationToken.None));
        Assert.Equal(role.Id, change.RoleId);
        Assert.Equal("author", change.RoleName);
        Assert.Equal(DateTimeKind.Utc, change.ChangedAtUtc.Kind);   // {now} renders the §7.9 marked text

        Assert.Equal("DDL-001", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.CreateTriggersAsync<DailySales>(CancellationToken.None))).Code);
    }

    [Fact]
    public async Task The_sample_history_creates_the_trigger_guards_it_and_derives_its_rollback()
    {
        await using var db = await OpenAsync();
        var runner = new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations");
        Assert.Equal(13, await runner.MigrateAsync(CancellationToken.None));
        var status = await runner.StatusAsync(CancellationToken.None);
        Assert.Contains(status, e => e is { Version: 13, ObjectName: "trg_roles_audit", State: MigrationState.Applied });

        // The trigger is live and fires.
        var role = (await db.QueryAllAsync<Role>(CancellationToken.None))[0];
        role.Name = "owner";
        await db.UpdateAsync(role, CancellationToken.None);
        Assert.Single(await db.QueryAllAsync<RoleChange>(CancellationToken.None));

        // Rolling back V0013 derives the drop from the snapshot (no Down anywhere).
        Assert.Equal(1, await runner.MigrateDownAsync(12, CancellationToken.None));
        Assert.Equal(0, await CountTriggersAsync(db));
        Assert.Equal(1, await runner.MigrateAsync(CancellationToken.None));
        Assert.Equal(1, await CountTriggersAsync(db));

        // Hand-changed outside the code: the rollback's guard refuses (MIG-012), force proceeds with notice.
        await SchemaSync.ApplyAsync(db, ["drop trigger trg_roles_audit", "create trigger trg_roles_audit after update on roles for each row begin select 1; end"], CancellationToken.None);
        Assert.Equal("MIG-012", (await Assert.ThrowsAsync<SimpleOrmException>(() => runner.MigrateDownAsync(12, CancellationToken.None))).Code);
        var notices = new List<string>();
        Assert.Equal(1, await runner.MigrateDownAsync(12, allowViewDrift: true, notices.Add, CancellationToken.None));
        Assert.Contains("trg_roles_audit", Assert.Single(notices));
        Assert.Equal(0, await CountTriggersAsync(db));
    }

    [Fact]
    public async Task A_change_step_guards_the_previous_declaration_and_a_multi_event_trigger_round_trips()
    {
        await using var db = await OpenAsync();
        var v1 = new SqlVersion(1,
            new SqlVersion.Step("widgets", "create", ["create table widgets (id INTEGER PRIMARY KEY, name TEXT) STRICT"]),
            new SqlVersion.Step("trg_widgets", "create", [], trigger: Declare(TriggerEvent.Update, "update widgets set name = name;")));
        var v2 = new SqlVersion(2,
            new SqlVersion.Step("trg_widgets", "change", [],
                trigger: Declare(TriggerEvent.Update | TriggerEvent.Delete, "select 1 from {old: id} o;"),
                expectTrigger: Declare(TriggerEvent.Update, "update widgets set name = name;")));

        await new MigrationRunner(db, [v1]).MigrateAsync(CancellationToken.None);
        Assert.Equal(1, await CountTriggersAsync(db));

        Assert.Equal(1, await new MigrationRunner(db, [v1, v2]).MigrateAsync(CancellationToken.None));
        Assert.Equal(2, await CountTriggersAsync(db));   // one per event on SQLite
        using (var connection = new SqliteConnection($"Data Source={_path}"))
        {
            connection.Open();
            var live = SqliteTriggerDdl.Read(connection, "trg_widgets");
            Assert.NotNull(live);
            Assert.Equal(TriggerEvent.Update | TriggerEvent.Delete, live.Definition.Events);
            Assert.Equal("select 1 from {old: id} o;", live.Definition.NormalizedSql);
        }

        // A second run of V2 against a drifted trigger: hotfix, then MIG-012.
        var v2b = new SqlVersion(3,
            new SqlVersion.Step("trg_widgets", "change", [],
                trigger: Declare(TriggerEvent.Update, "select 2;"),
                expectTrigger: Declare(TriggerEvent.Update | TriggerEvent.Delete, "select 1 from {old: id} o;")));
        await SchemaSync.ApplyAsync(db, ["drop trigger trg_widgets_delete"], CancellationToken.None);
        Assert.Equal("MIG-012", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => new MigrationRunner(db, [v1, v2, v2b]).MigrateAsync(CancellationToken.None))).Code);
        Assert.Equal(1, await new MigrationRunner(db, [v1, v2, v2b]).MigrateAsync(allowViewDrift: true, null, CancellationToken.None));
        Assert.Equal(1, await CountTriggersAsync(db));
    }

    [Fact]
    public async Task The_sqlite_read_back_merges_per_event_triggers_only_when_they_form_one_declaration()
    {
        await using var db = await OpenAsync();
        await SchemaSync.ApplyAsync(db, ["create table widgets (id INTEGER PRIMARY KEY, name TEXT) STRICT"], CancellationToken.None);

        // The narrowing lives on the update trigger, which sorts after the delete one by name.
        var narrowed = new TriggerDefinition("trg_w", TriggerTiming.After, TriggerEvent.Update | TriggerEvent.Delete, "select 1 from {old: id} o;", ["name"]);
        await SchemaSync.ApplyAsync(db, new SqliteDialect().CreateTriggerSql("widgets", false, narrowed), CancellationToken.None);

        // Per-event triggers that disagree (another body on one event) were not written from one declaration.
        await SchemaSync.ApplyAsync(db,
        [
            "create trigger trg_x_update after update on widgets for each row\nbegin\nselect 1;\nend",
            "create trigger trg_x_delete after delete on widgets for each row\nbegin\nselect 2;\nend",
        ], CancellationToken.None);

        using var connection = new SqliteConnection($"Data Source={_path}");
        connection.Open();
        var live = SqliteTriggerDdl.Read(connection, "trg_w", out var note);
        Assert.NotNull(live);
        Assert.Null(note);
        Assert.Equal(["name"], live.Definition.UpdateOf);
        Assert.Equal(TriggerEvent.Update | TriggerEvent.Delete, live.Definition.Events);

        Assert.Null(SqliteTriggerDdl.Read(connection, "trg_x", out note));
        Assert.Contains("do not form one declaration", note);
        Assert.Null(SqliteTriggerDdl.Read(connection, "trg_absent", out note));
        Assert.Null(note);
    }

    [Fact]
    public void The_scan_counts_an_unlinked_declaration_class_for_the_name_rule()
    {
        var conflict = Assert.Single(EntityMapLoader.TriggerConflicts([typeof(SharedOwnerA), typeof(SameNameTrigger)]));
        Assert.Equal(("MAP-029", "trg_shared"), (conflict.Code, conflict.Target));
    }

    private static TriggerSnapshot Declare(TriggerEvent events, string sql)
        => new("widgets", false, new TriggerDefinition("trg_widgets", TriggerTiming.After, events, sql));

    private static readonly Query<EmptyArgs, long> CountTriggers = Query.Inline("select count(*) from sqlite_master where type = 'trigger'");

    private static async Task<long> CountTriggersAsync(Db db)
        => await db.QuerySingleAsync(CountTriggers, EmptyArgs.Value, CancellationToken.None);
}
