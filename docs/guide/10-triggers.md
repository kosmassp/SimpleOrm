# Triggers

A trigger is its own declaration class carrying one body, the way a
materialized view carries its SQL; the entity links it. Nothing in the
declaration names a dialect: the body is set-based SQL with three tokens, and
each dialect renders the header, the wrapper, the granularity, and the token
expansions. Migrations own triggers as an object kind. The rules are in
[`spec/metadata-model.md`](../../spec/metadata-model.md#triggers-level-4-adr-0042),
[`spec/migrations.md`](../../spec/migrations.md), and
[`spec/dialects.md`](../../spec/dialects.md).

## The worked example: `RoleAuditTrigger`

After every update of a role, one audit row per changed role goes into
`role_changes`:

```csharp
[Trigger("trg_roles_audit", TriggerTiming.After, TriggerEvent.Update, """
    insert into role_changes (role_id, role_name, changed_at)
    select n.id, n.role_name, {now}
    from {new: id, role_name} n;
    """)]
public sealed class RoleAuditTrigger { }

[Table("roles")]
[Trigger(typeof(RoleAuditTrigger))]
public sealed class Role : BaseModel { ... }

[Table("role_changes")]
public sealed class RoleChange
{
    [Key] [Generated] [Column] public long Id { get; set; }
    [Column] public long RoleId { get; set; }
    [Column] public required string RoleName { get; set; }
    [Column("changed_at")] public DateTime ChangedAtUtc { get; set; }
}
```

`RoleChange` is an ordinary table entity the library only reads; the trigger
is its writer, so it carries no audit hooks.

## The declaration

`[Trigger(name, timing, events, sql)]` on the trigger class, exactly one:

- **name**: the trigger name exactly as it exists in the database; unique
  across the model.
- **timing**: `TriggerTiming.Before`, `After`, or `InsteadOf`.
- **events**: `TriggerEvent.Insert`, `Update`, `Delete`, combinable with `|`.
- **sql**: the body, each statement ended by `;`.
- `UpdateOf = [nameof(Role.Name)]` narrows an update trigger to the listed
  properties (resolved to columns; only with the `Update` event).

The class is a declaration object, never an entity: no columns, no key; scans
skip it.

## The link

`[Trigger(typeof(X))]` on the entity, repeatable: a table carries many
triggers, a trigger belongs to one table. A table takes every timing; a view
takes `InsteadOf` only; no other source takes a trigger.

## The body tokens

| Token | Meaning | SQLite, PostgreSQL | SQL Server |
|---|---|---|---|
| `{new: a, b}` | the inserted or updated rows as a relation | `(select new.a as a, new.b as b)` | `inserted` |
| `{old: a, b}` | the deleted or pre-update rows | the same over `old.` | `deleted` |
| `{now}` | UTC now in the storage form | `strftime('%Y-%m-%dT%H:%M:%fZ', 'now')` / `now()` | `todatetimeoffset(sysutcdatetime(), 0)` |

The column list is required and names column names of the entity's relation.
A row token must have rows on every declared event, since one body serves them
all: `{new}` refuses a declaration that includes delete, `{old}` one that
includes insert. Braces inside a single-quoted literal are text. A `--` line
comment is refused (snapshots collapse whitespace and would fold the rest of
the body into it); use `/* ... */`.

## What each dialect renders

From `conformance/trigger-sql/after_update_audit.json`, the one declaration
above becomes:

SQLite, one row-level trigger (multi-event declarations render one trigger per
event, named `<name>_<event>`):

```sql
create trigger trg_roles_audit after update on roles for each row
begin
insert into role_changes (role_id, role_name, changed_at)
select n.id, n.role_name, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
from (select new.id as id, new.role_name as role_name) n;
end
```

SQL Server, one statement-level trigger with `set nocount on`:

```sql
create trigger [trg_roles_audit] on [roles] after update as
begin
    set nocount on;
insert into role_changes (role_id, role_name, changed_at)
select n.id, n.role_name, todatetimeoffset(sysutcdatetime(), 0)
from inserted n;
end
```

PostgreSQL, a PL/pgSQL function plus the trigger:

```sql
create or replace function "trg_roles_audit_fn"() returns trigger language plpgsql as $$
begin
insert into role_changes (role_id, role_name, changed_at)
select n.id, n.role_name, now()
from (select new."id" as "id", new."role_name" as "role_name") n;
    return null;
end
$$;
create trigger "trg_roles_audit" after update on "roles" for each row execute function "trg_roles_audit_fn"()
```

A form a dialect lacks is refused when rendered, not at declaration, with
`DDL-005`: `Before` on SQL Server, `InsteadOf` on a table anywhere but SQL
Server (SQLite and PostgreSQL allow it on views only). Every create is
drop-if-present then create, so repeating it is safe.

## Creating triggers in dev and test

`CreateTriggersAsync<T>` creates an entity's declared triggers from metadata,
separately from `CreateTableAsync` and never implicitly:

```csharp
await db.CreateTableAsync<Role>(ct);
await db.CreateTableAsync<RoleChange>(ct);
await db.CreateTriggersAsync<Role>(ct);
await db.CreateTriggersAsync<Role>(ct);   // replace semantics: idempotent

var role = new Role { Name = "editor", CreatedAtUtc = now };
await db.InsertAsync(role, ct);           // an insert does not fire an update trigger
role.Name = "author";
await db.UpdateAsync(role, ct);
var change = Assert.Single(await db.QueryAllAsync<RoleChange>(ct));
```

`CreateTriggersAsync` on an entity that is not a table or view (the statement
`DailySales`, say) is `DDL-001`. In PHP: `$db->createTriggers(Role::class)`.
In Go: `orm.CreateTriggers[Role](ctx, db)`.

## Migrations own the trigger

The object kind is `Trigger/<Class>/`; the `schema_version` object is the
trigger name. A step names the entity and the trigger class, and every action
renders through the dialect from a declaration. There is no raw-SQL trigger
action: trigger DDL is dialect-specific, and a step holding one dialect's text
would tie the tree to it.

```csharp
// Migrations/Trigger/RoleAuditTrigger/V0013_CreateRoleAudit.cs
public sealed class V0013_CreateRoleAudit : TriggerMigration<global::SimpleOrm.Sample.Models.Role, global::SimpleOrm.Sample.Models.RoleAuditTrigger>
{
    public override void Action(TriggerActions actions) => actions.CreateTrigger();
}

// Migrations/V0013.cs: the table first, the trigger after
public sealed class V0013 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.RoleChange.V0013_CreateRoleChanges>()
        .Apply<Trigger.RoleAuditTrigger.V0013_CreateRoleAudit>();
}
```

The actions:

- `CreateTrigger()`: the initial create, from the entity's linked declaration
  (`DDL-001` when the entity does not link the class).
- `Recreate(timing, events, sql, updateOf...)`: a literal, frozen declaration;
  what `diff` emits for a change.
- `ExpectDefinition(timing, events, sql, updateOf...)`: the `MIG-012` guard; the
  live trigger must be exactly this declaration's rendering, else the run
  refuses (`--force` recreates with notice).
- `DropTrigger()`: the removal.

The snapshot is the neutral declaration, so one tree serves every dialect:

```json
{
  "object": "trg_roles_audit",
  "kind": "trigger",
  "relation": "roles",
  "relationKind": "table",
  "asOfVersion": 13,
  "generatedAt": "2026-10-22T12:00:00.0000000Z",
  "timing": "after",
  "events": [ "update" ],
  "sql": "insert into role_changes (role_id, role_name, changed_at) select n.id, n.role_name, {now} from {new: id, role_name} n;"
}
```

`diff` compares declarations: a different timing, event set, update-of list,
relation, or whitespace-normalized body is a change; a snapshot history no
entity declares any more is a removal, gated by `--allow-remove`. A removal
step needs the trigger class and its entity still in the assembly; keep them
until the removal has applied. Renaming a trigger is a removal plus a creation;
moving one to another relation is `DDL-004` and written by hand. `migrate down`
derives the rollback (the guard, the drop, the previous declaration). `shadow`
parses the stored SQLite trigger back to its declaration.

In PHP the step declares `entityClass()` and `triggerClass()`, the declaration
is `#[Trigger('trg_roles_audit', TriggerTiming::After, [TriggerEvent::Update], <<<'SQL' ... SQL)]`
with events as an array, and the link `#[Trigger(RoleAuditTrigger::class)]`.
In Go the trigger type's `Trigger()` method returns an `orm.TriggerDef{Name, Timing: orm.TimingAfter, Events: orm.EventUpdate, SQL}`,
the entity links it with `Triggers: []orm.TriggerLink{orm.Trigger[RoleAuditTrigger]()}`,
the step embeds `orm.TriggerMigration[sample.Role, sample.RoleAuditTrigger]`,
and the registry lists the class under `Triggers`.

## Pitfalls

- `MAP-029`: a link on a statement, procedure, or materialized view; a link to
  a class with no declaration, with two, or that is itself an entity; a blank
  name, no event, or a blank body; `Before`/`After` on a view; `UpdateOf`
  without the `Update` event or naming an unmapped property; an unknown or
  malformed token, a row token without columns or naming an unmapped column,
  `{new}` with a delete event or `{old}` with an insert event; a `--` comment;
  a class linked by two entities or two classes declaring one name.
- `DDL-005`: `Before` on SQL Server, `InsteadOf` on a table outside SQL
  Server. The declaration is fine; that dialect has no form for it.
- `DDL-001`: `CreateTrigger()` in a step whose entity does not link the class,
  or `CreateTriggersAsync` on a statement entity.
- `MIG-012`: the live trigger differs from what the step expected (changed by
  hand). Review, then `migrate --force` if the code should win.
- The body may not contain `$$` (PostgreSQL's function quoting).
- The SQL Server affected-row rule exists because of triggers: without it a
  trigger's rows would let a stale write pass as a success. It is on by
  default; see [CRUD](04-crud-and-concurrency.md#the-sql-server-affected-row-rule).
