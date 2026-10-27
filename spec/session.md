# Session — one connection, explicit transactions

The session (§7.17) is the unit of database access: it owns **one connection**,
at most **one transaction**, and no ambient state. Every round trip is visible in
user code — no hidden queries, no lazy proxies. Every I/O operation is async and
takes a cancellation token; that surface is the spec-level contract even where a
provider is synchronous underneath (ADR-0003).

## Lifetime

A session opens against a connection string plus options and ends by disposal:

```csharp
await using var db = await Db.OpenAsync(connectionString, options, ct);
```

- The dialect creates the connection (§7.25); the session opens it. A failed
  open leaves nothing behind — the connection is released and the error
  propagates. (An open the dialect calls transient may be attempted again
  under `OpenRetries` — "Resilience" below.)
- Options are fixed at open: the dialect (required), the mapping configuration
  (naming convention, explicit maps — metadata-model.md), and the type-handler
  registry (mapping-rules.md). The session owns its metadata cache: an entity's
  map loads once per session.
- Disposal rolls back any still-active transaction, then closes the connection.
  Disposal is the only teardown; there is no close-and-reopen.

Everything the session exposes — registry queries and commands, statement
entities, key reads, generated CRUD, criteria queries, metadata DDL — runs on
this one connection and automatically inside the current transaction, if any.

## The registry surface

A query or command is declared **once**, binding SQL to its argument and result
types (§6, §7.5); execution takes the entry, an args value, and a token. The SQL
is inline at the declaration site or an embedded resource, resolved lazily and
cached — a missing resource is `QRY-003` at first use, not at declaration. Every
entry carries a description (the resource path; a prefix of the inline SQL), and
every error about the entry names it.

- **Query** → all rows, fully materialized, in result-set order.
- **QuerySingle** → exactly one row: zero is `QRY-001`, more than one `QRY-002`.
- **QuerySingleOrDefault** → at most one row: zero yields the absent value
  (null; a value-typed result yields its default), more than one `QRY-002`.
- **Stream** → rows delivered one at a time as the caller iterates; nothing is
  buffered.
- **Execute** → a non-query command; returns the affected-row count.

Rows become objects through the one mapping pipeline (mapping-rules.md). The
result shape is validated from the result schema **before the first row**, so
strictness (`MAP-001/002/003`) fires even for an empty result — on streams,
before the first element is produced.

Parameterless SQL takes the canonical empty args value (`EmptyArgs`); with no
properties, parameter strictness is trivially satisfied.

## Statement-backed entities

For a statement-backed type the type *is* the query (ADR-0008/0010): execution
names the result type and passes args only —

```csharp
var days = await db.QueryAsync<DailySales>(new DailySalesArgs(since), ct);
```

- The target must actually be statement-backed; anything else is `QRY-004`.
- The loader already proved declaration ↔ SQL agreement (`PRM-010/011`,
  metadata-model.md). At execution the args are checked against the declaration
  by **type** as well as name: a property whose type differs from the declared
  parameter type is `PRM-012` (name matching case-insensitive, nullable
  wrappers transparent).
- Binding then proceeds against the statement's defining SQL under the same
  rules as registry entries (`PRM-001/002` below).
- Single / SingleOrDefault / Stream variants mirror the registry surface,
  including `QRY-001/002`.

The inverse rule: a source without a named relation (statement, procedure)
refuses select-all, key reads, and criteria queries with `QRY-005` — the
statement API is its only read path.

## Parameters

`@name` placeholders bind from the public readable properties of the args value
(§7.12/§7.13), matched case-insensitively. Placeholders inside string literals
and SQL comments are not placeholders. Both directions are strict, checked at
every execution:

- a placeholder with no matching property → `PRM-001`
- a property no placeholder uses → `PRM-002`

Values cross the boundary through the conversion pipeline (mapping-rules.md);
a conversion error names the query and the placeholder.

**System functions are not placeholders.** `@@name` (SQL Server's
`@@rowcount`, `@@identity`) is never a placeholder: a placeholder is an `@`
not preceded by another `@`. (Found by both ADR-0044 ports: the second `@` of a
generated `select @@rowcount` read as the placeholder `@rowcount`, and a
registry command using a system function was `PRM-001` in the reference.)

**Collections (IN lists).** A collection-typed property (strings and byte
arrays are values, not collections) expands its placeholder into one generated
placeholder per element — `IN (@Ids)` becomes `IN (@Ids_0, …, @Ids_N)` — each
element bound as a parameter. Only placeholder *names* are written into the
SQL; values never are (§2: no SQL from user data by concatenation, ever). An
**empty** collection renders the placeholder as SQL `NULL`: `x IN (NULL)` is
valid and matches no rows — an empty list selects nothing, it never errors.
Every occurrence of the placeholder expands identically.

Expansion is the no-array-parameters strategy (`SupportsArrayParameters`,
§7.12, ADR-0003): SQLite has none, so the reference implementation always
expands. A dialect with native array parameters (ADR-0025 Postgres) binds the
collection as a **single typed-array parameter** with the SQL untouched — the
registry SQL is written dialect-natively (`WHERE id = ANY(@ids)`), every
element passes through the same conversion pipeline, and an **empty**
collection binds a typed empty array (the element's database type still
matters: `bigint = any(text[])` refuses even empty). The observable contract —
always parameterized, empty matches no rows — is identical either way.

## Transactions

```csharp
await using var tx = await db.BeginAsync(ct);
// ... commands enlist automatically ...
await tx.CommitAsync(ct);
```

- One transaction per session: `BeginAsync` while one is active is `TX-001`.
  There is no nesting; savepoints are Level 4 (§7.17).
- Enlistment is automatic and total: from begin until commit or rollback, every
  operation on the session runs inside the transaction. There are no ambient,
  static, or thread-bound transactions — the scope is a value the caller holds.
- Commit is explicit. Rollback happens three ways: an explicit rollback,
  disposing an uncommitted scope, or disposing the session while its
  transaction is active. An uncommitted transaction is never silently
  committed.

## Async and cancellation

Every I/O operation is asynchronous and takes a cancellation token — the
contract (§2) that ports implement in their own idiom, even where the provider
is synchronous underneath (ADR-0003: the SQLite provider is; the async surface
is what keeps one shape across ports and future dialects).

- Cancellation is cooperative: observed when a statement starts, periodically
  between rows during materialization, and per row on streams.
- Cancellation surfaces as the platform's cancellation signal, never as a
  SimpleOrm error code.

## Batching — messages, the option, the explicit batch (Level 4, ADR-0044)

A **message** is what one round trip sends to the database. Without batching
every statement is a message of its own. With batching, the statements of
**one call** — a `Flush` (unit-of-work.md "Batching") or the `Send` of an
explicit batch (below) — go out in as few messages as the driver allows.
Nothing waits and nothing is deferred past the call that holds the statements:
there is no timer and no implicit queue, a lone statement runs exactly as it
always did, and every round trip stays a visible call in user code (§2).

**The option**, fixed at open like tracking:

- `Batching`: `Off` (the default) or `Auto`. `Off` is the Level 0–3 behaviour
  exactly — one message per statement. `Auto` forms messages by the plan below.
- `MaxStatementsPerMessage`: the most statements one message carries; 100 by
  default. A value below 1 is the language's argument error at open.
- `BatchingInEffect`, a read-only question on the session: true when the option
  is `Auto` and this implementation's driver for the dialect sends a planned
  message as **one** round trip with a result per statement
  (`SupportsStatementBatching`, spec/dialects.md "Statement batching"). Where it
  is false under `Auto` — SQLite in every implementation, PostgreSQL in PHP, the
  C# `netstandard2.0` build — the plan is formed all the same and each message's
  statements run one at a time, in order, **reusing one prepared command per
  distinct statement text** within the call (on SQLite, where no network round
  trip exists, that reuse is the gain: ADR-0044 measures it). Nothing refuses:
  one configuration serves every database, and the question says which
  realization is in force.

**The plan** — how a call's statements become messages. The statements are the
ones the call issues without batching, in the same order: batching never
reorders, adds, or drops a statement and never changes its SQL text. Walking
them in order, a statement **starts a new message** when the current message is
not empty and

1. **it depends on the current message** — decided *before* the statement is
   prepared, because preparing reads what the earlier statements produce:
   a. the current message holds a statement on the **same instance**, or on an
      instance of the **same entity type with the same key** — when that key is
      set (not null, not a database-generated key at its default, not an empty
      client GUID — the "unset key" of unit-of-work.md "Detach and merge"); the
      new statement's key is taken as it stands before its preparation (a key
      the flush is about to flow in is not there yet), the message's as its
      statements were prepared; a delete by key compares its key argument; or
   b. the statement's instance has a non-null **many-to-one navigation** — or,
      in a flush, a **cascade owner** it takes its key from (unit-of-work.md
      "Keys flow before each insert") — pointing at an instance whose insert is
      in the current message and **whose key the database generates**;
2. **it reads a result after a registry command** — the statement returns a
   result the session reads (an insert whose key the database generates; where
   the dialect reports affected rows by select, `ReportsAffectedRowsBySelect`, a
   generated update or delete) and the current message holds a registry
   command, whose own result sets, should its SQL return rows, would otherwise
   come first;
3. **it would overflow the message** — the message already holds
   `MaxStatementsPerMessage` statements, or the dialect caps the parameters of a
   message (`MaxParametersPerMessage`; SQL Server 2098) and the statement's bound
   parameters would take the message over the cap. A statement alone over the
   cap is a message of its own.

A message that holds `MaxStatementsPerMessage` statements goes out **as soon as
it is full** — before the next statement is prepared, like a rule-1 cut; rule 2
and the parameter cap are decided after the statement is prepared. An operation
that turns out to issue no statement (an explicit batch's clean tracked update)
adds nothing to the message, but still ended the current one if rule 1 said so.
Under `Off` the cap is one: every statement is prepared and run before the next
is prepared, exactly as the immediate operations.

The plan is a property of the session, not of the driver: it reads the
statements and two facts of the dialect — whether a generated update or delete
returns its count as a result (`ReportsAffectedRowsBySelect`, rule 2) and the
parameter cap (rule 3) — so every implementation forms the same messages from
the same statements on the same dialect, whatever its driver can do;
`batch-cases/` pins them under the SQLite dialect's facts (no reported counts,
no cap). A realization may use **more** round trips than the
plan's messages — the sequential one does, and a driver that cannot carry a
particular statement in a message (spec/dialects.md) sends that statement on its
own — never fewer.

**Results and checks inside a message.** Each statement's result is read per
statement, in order — the generated key of an insert, the affected-row count of
a generated update or delete (the driver's, or the dialect's reported count,
spec/crud.md), the driver's count of a registry command. The statements are then
judged and applied in order exactly as their immediate operations would be: keys
written onto the entities, zero rows `CRUD-010`/`CRUD-001`, versions bumped,
hooks, the identity map and the snapshots updated. The first statement that
fails its check fails the call.

**Failure.** A zero-row update is no error to the database, so the statements
after a failing check in the same message **may have run** (they did wherever
batching is in effect; the sequential realization stops at the failure). The call
fails as a whole:

- every in-memory effect of every statement the call prepared is undone — keys,
  versions, stamps, flags, tracking — so the instances are as they were before
  the call;
- the call's own transaction rolls back: nothing applied;
- inside the caller's transaction, the rows of the statements that ran — before
  the failure and possibly after it in its message — are the caller's to roll
  back, and the caller must: a commit would keep rows the session does not know.
  (Without batching, only the statements before the failure had run.)

A provider error (a constraint, a deadlock) fails the call the same way and
propagates unchanged; cancellation stays the platform's signal.

### The explicit batch

```csharp
await using var batch = db.Batch();              // queuing does no I/O
batch.Insert(order);
batch.UpdateOnly(user, [nameof(User.Name)]);
batch.Execute(Commands.MarkShipped, new MarkShippedArgs(Id: 7));
batch.Delete<Role>(3);
BatchResult result = await batch.SendAsync(ct);  // the visible round trip(s)
```

- `Batch()` returns a queue bound to the session. `Insert`, `Update`,
  `UpdateOnly`, `Delete` (the key or the entity form, dispatched as the immediate
  `Delete` is), and `Execute` (a registry command and its args) queue one
  operation each. Queuing does no I/O, binds no value, and checks nothing but the
  batch's own state: whatever the immediate call would refuse, `Send` refuses.
- `Send` runs the queued operations in queue order, as messages by the plan.
  **Each behaves exactly as its immediate call would at that point of the
  sequence** — the same checks when it is prepared (`CRUD-002` to `CRUD-007`,
  `UOW-002`, `CRUD-004`, `PRM-001`/`PRM-002`, the converter's codes), the same
  statement, the same effects on the instance and the session (tracking, hooks,
  versions). Keys do not flow between queued operations — the immediate calls
  never flow them; the foreign-key property is what is written — the unit of
  work is the form that flows keys.
- `Send` always runs in a transaction: the session's when one is active, else one
  it begins and commits after the last message (a SQL Server batch outside a
  transaction would commit statement by statement). It fails as the plan fails.
  The queue is spent either way.
- The result carries `Affected` — one count per queued operation, in queue
  order: an insert 1; a generated update or delete its judged count; a registry
  command the driver's count; an operation that issued no statement (a clean
  tracked update) 0 — and `Messages`, the number of messages the plan formed.
- A batch that was sent, or disposed, takes nothing more: queuing on it or
  sending it again is `TX-003`. A batch disposed unsent is dropped silently —
  nothing is ever sent implicitly, as with scheduled work. An empty send returns
  an empty result and begins no transaction.
- Under `Off` the batch exists and behaves the same, one statement per message:
  switching the option never requires a code change.
- **Reads are not batched.** A batch queues writes and registry commands only;
  batched reads (a handle filled at send, each result through the one mapping
  pipeline) are deferred (ADR-0044).

Where a language has no overloading the operations keep these names as the
immediate calls do: PHP `$db->batch()`, `insert`, `update`, `updateOnly`,
`delete`, `execute`, `send`, and `dispose()` for disposal; Go `db.Batch()` with
the package functions `orm.BatchInsert`, `BatchUpdate`, `BatchUpdateOnly`,
`BatchDelete[T]`, `BatchDeleteEntity`, `BatchExecute` (generic operations cannot
be methods in Go, as for the immediate calls), `b.Send(ctx)`, and `b.Discard()`
as the analog of disposal.

**Clarifications** (the PHP and Go port gap lists of ADR-0044, 14 + 16
questions, consolidated; each the answer the three implementations now embody):

1. **A message's statements are all prepared before it is sent.** A refusal at
   preparation (`CRUD-004`, `UOW-002`, a converter's code) therefore fails the
   call before the message holding the earlier statements runs. When two
   statements of one call would both fail — an earlier one at execution
   (`CRUD-010`), a later one at preparation in the same message — `Auto` reports
   the preparation's code where `Off` reports the execution's. Either way the
   call fails and nothing applies.
2. **Prepared-command reuse is `Auto`'s.** Under `Off` every statement is
   prepared fresh — the Level 0–3 path exactly.
3. **Rule 3 counts bound parameters**: the values bound to the prepared
   statement, after IN-list expansion — for a generated statement the same as
   its placeholders.
4. **Rule 1a and the key.** The same-key clause keeps a planned call equal to
   its immediate calls where two instances share a key: a delete of A followed by
   an insert of B with A's key (`Off` succeeds — A is forgotten before B's
   identity gate), two new instances with one natural key (`Off` refuses the
   second with `UOW-002`, not the database's key violation). Without it the
   identity gate would read a map the current message has not yet updated.
5. **Rule 1b for every statement.** It applies to a statement on any instance
   with such a navigation — inserts, updates (their `CRUD-004` check reads the
   target's key), and deletes alike. The cascade owner is the one a persist
   cascade recorded through a one-to-many or one-to-one navigation, whose key the
   child takes.
6. **The message hook and `Messages`** count the plan's messages, not round
   trips: a realization that splits a message adds none. The hook is told a
   message's size as it is sent, before its outcome is known, so a message that
   fails is counted; a call with nothing to send tells it nothing. Under `Off`
   `Messages` equals the number of statements.
7. **A send whose operations all issue no statement** (clean tracked updates)
   still begins and commits a transaction; only an empty queue begins none. It is
   unobservable outside the transaction.
8. **The option is validated at open**, before the connection opens, so a refused
   open leaves nothing behind. Where the language's zero value cannot tell
   "unset" from 0 (Go), 0 means the default of 100 and a negative value is
   refused; the port records it in its §10.
9. **A key flowed into a foreign-key property stays after a failed flush** (as
   before batching): the flush copies parents' keys into children before each
   insert's preparation, and the undo restores what the statements did — keys,
   versions, stamps, flags, tracking — not the copied foreign keys.
10. **In the step scripts an unset integer key reads as 0** (`"equals": 0`),
    whatever the language's own representation of "unset" (a PHP typed property
    with no value, a Go zero value).
11. **The keys rule 1a compares** (the review round). Every statement that has an
    instance or a key argument carries its key — inserts, updates, deletes by
    entity and by key alike. A delete by key's argument is compared after the
    same validation and coercion the delete applies (an `int` matches an `int64`
    key); an argument that does not validate carries no key and is refused when
    it is prepared. A client-GUID insert's key counts once its preparation has
    filled it, so two inserts that both arrived with an empty GUID never cut each
    other.

## Statement log (Level 4 item 4, ADR-0045)

The smallest observability there is: the session can tell the application
**what it sends to the database**, one line of text per statement, as the
statement is sent. The library brings no logging dependency; the application
forwards the line to its own logger, or to standard output, or asks for
nothing.

**The option**, fixed at open like the others:

- `Log`: a function of one string, called once per statement with the line
  below. Unset (the default), nothing is written and nothing is formatted —
  the Level 0–3 behaviour exactly. Every implementation ships one built-in
  sink that writes each line to standard output (C# `DbLog.Console`), so a
  trial switches the log on with one assignment; forwarding to the
  application's logger is the application's own one-line adapter
  (`line => logger.LogDebug(line)`), never a library concern.
- `LogParameterValues`: whether the line carries the parameter **values**
  beside their names. **Off by default**: values may be personal data, and a
  log never receives them unless the application asks (§2 "strict by
  default", applied to what leaves the process).

**The line.** `<name>: <sql>`, followed, when the statement has parameters,
by a space and the parameter names in bind order in square brackets —
`[@k0]`, `[@Name, @Id]` — or, with values on, `[@Name = 'Ada', @Id = 7]`.
The SQL is the text **exactly as sent**: after IN-list expansion
("Parameters"), the dialect's rendering (`set nocount on; …` on SQL Server,
`returning`), and whatever the realization sent (a multi-statement text is
one line per statement it holds, as the plan counts them). Nothing is
reformatted or truncated. A value is written **as it was bound** — after
the converter, so what the driver receives (a PHP bool is the `0`/`1` it
binds; a decimal the text it binds) — in a fixed, culture-neutral form: a
string single-quoted with `'` doubled, a null as `null`, a bool as
`true`/`false`, bytes as `0x` + the hex of the first 16, an ellipsis `…`
when there are more, and always ` (N bytes)` — `0x0102 (2 bytes)`,
`0xabab…abab… (20 bytes)` —, an instant as the dialect binds it (ISO-8601
text on SQLite), an array parameter in square brackets; numbers and the rest
in their invariant form.

**The name** says what issued the statement:

| statement | name |
|---|---|
| a registry query or command | the name the registry declares — `Queries.UserByEmail` — where the implementation knows it (C# and PHP learn it when SchemaGuard enumerates the registry, which an application does at startup, §7.18; Go's explicit registry has no variable names); otherwise the resource path of an embedded entry; otherwise the kind of entry, `query` or `command` (an inline entry's description would only repeat the SQL) |
| a generated write | `<Entity>.insert`, `<Entity>.update` (the full-row and the tracked dirty update alike), `<Entity>.updateOnly`, `<Entity>.delete` (the key form, the entity form, and the soft delete's flag-setting update alike — the name is the operation asked for) |
| a generated read | `<Entity>.get` (the key read, `Refresh` included), `<Entity>.criteria` (criteria queries, and the queries explicit and batch loading issue — a many-to-many load is the link entity's criteria, then the target's), `<Entity>.include` (a join-mode eager query), `<Entity>.selectAll`, `<Entity>.statement` (a statement-backed entity) |
| metadata DDL | `<Entity>.createTable`, `<Entity>.createIndex`, `<Entity>.createView`, `<Entity>.createTrigger` (its drop statements too) |
| the migration runner | `migrate` — every statement it runs: the version table, the recorded history, the steps, the guards' definition reads, the rollback |
| SchemaGuard | `validate <source>` — the describe call of a registry entry (its name as the registry declares it, else the fallback of the first row) or of a statement-backed entity, and the column introspection it causes, under the same source; `<source>` for an entity's own check is the class name. SchemaGuard's `MIG-030` check reads the history through the runner: `migrate` |
| the schema tooling | `shadow` (everything a shadow run sends: the replayed steps, its introspection, the view and trigger read-back — the runner's statements take the shadow's name there), `sync` (the force-sync introspection and the plan's statements; the `migrate --force` run before it stays `migrate`) |

`<Entity>` is the class name as the fixture spells it (the step scripts'
names). Where a language spells a generated operation differently
(`updateOnly`, `BatchUpdateOnly`), the log keeps the reference's
spelling: the line is for reading across implementations.

**When.** The line is written **as the statement is sent**: after it is
prepared and bound — so what the application reads is what the database
receives — and before it executes. Exactly one line per statement sent, in
the order sent; a message of several statements (ADR-0044) writes one line
per statement, in statement order, before the message goes out. A statement
refused before it is sent (`CRUD-004`, `UOW-002`, `PRM-001`, a converter's
code, a `MAP-028` type) writes nothing: no SQL left the session. The line
says nothing about the outcome — not the duration, not the count, not the
error — the error reaches the caller as it always did; a second, after-the-
fact line is a possible later addition, not part of this. The log function
runs on the calling thread, synchronously; an exception it throws fails the
call like any other exception — the library never swallows it.

**What is not a statement of the application's**: the dialect's own
housekeeping inside the seam — the migration run lock (`begin immediate`,
`sp_getapplock`, `pg_advisory_xact_lock`), the connection open, the shadow
host's `create database`/`drop database` on the admin connection — and
transaction control (`begin`, `commit`, `rollback`, and the savepoints the
library itself issues around SchemaGuard's shield and the PostgreSQL view
probe: `savepoint`, `release`, `rollback to`), whether the provider or the
library issues it. They are not logged.

**The CLI** takes `--log`: every statement of a `migrate`, `migrate down`,
`shadow`, `diff --amend --db`, `validate`, or force-sync run goes to standard
output through the built-in sink.

**Per language.** PHP and Go keep the option's meaning and the line's
content; the shape of the function and the sink are theirs and recorded in
their §10 (PHP: `new DbOptions(..., log: ?Closure, logParameterValues: bool)`
and `DbLog::stdout()`; Go: `orm.Options{Log func(string), LogParameterValues
bool}` and `orm.ConsoleLog`, with `Named(...)` on an entry and
`migrations.WithStatementLog` for the tooling, Clarifications 1 and 9). The
line's text is per language and is **not**
pinned by a conformance file: each implementation proves in its own tests
that every execution path writes exactly one line with its name, that the
flush and the batch write one per statement under `Off` and `Auto`, that
values appear only when asked, and that the built-in sink writes to standard
output.

**Clarifications** (the PHP and Go port gap lists of ADR-0045, 17 + 22
questions, consolidated; each the answer the three implementations now
embody):

1. **A registry entry's name, where the implementation can learn it.** The
   reference stamps the name onto the entry object when SchemaGuard
   enumerates the registry. Where entries are values built per call (PHP's
   registry methods return a fresh `Query` each time), the session may key
   the learned names by (kind, SQL text) — two entries with one SQL text
   keep the first name. Where nothing can be learned (Go's explicit registry
   of package variables), the entry may **declare** its name
   (`orm.Inline[...](sql).Named("Queries.UserByEmail")`), optional, and the
   declared name wins everywhere, SchemaGuard's `validate <name>` lines
   included. The spelling is `<Class>.<member>` as the registry writes them
   (`Queries.UserByEmail`, `Queries.userByEmail`), never normalized.
2. **The fallback name is the kind.** An entry whose name the implementation
   does not know logs as `query` or `command` — in SchemaGuard's lines too
   (`validate query`) — or as its resource path when it is embedded (the
   `embed.FS` path in Go). An inline entry's description is never used: it
   repeats the SQL.
3. **"Exactly as sent" means as the library hands it to the driver.** A
   driver's own rewrite is invisible — PDO's `:name`, pgx's `$n` — so the
   line shows the `@name` text and the lines read the same across languages.
   A rewrite the **library** makes is visible: the SQL Server multi-statement
   realization (spec/dialects.md) logs each statement with its placeholders
   renamed apart (`@p0_name`, `@p1_name`), one line per statement, because
   that is the text it sends.
4. **"Before the message goes out" is for a message sent as one round trip**
   (`DbBatch`, pgx `SendBatch`, the multi-statement text): its lines are
   written together, in statement order, after the message is rendered and
   before it is sent. The **sequential realization** writes each line right
   before its statement runs, so after a failure the statements that never
   ran have no line — "exactly one line per statement *sent*".
5. **Where preparing is itself a round trip** (PDO on PostgreSQL and SQL
   Server, prepared statements generally), the line is written after the
   library's binding and before the execute call; a statement whose prepare
   fails wrote nothing. The one exception is SchemaGuard's describe, where
   the prepare *is* the call: its line precedes it, so an unparseable entry
   (`VAL-001`) still shows what was described. A registry command is
   described too (prepared, never run) and logs the same way; the describe
   line lists the parameter names the implementation binds for it (none
   where it describes unbound).
6. **A stream's line is written when iteration starts**, not when the
   stream object is created: that is when the statement is sent.
7. **Eager loading's queries are criteria queries.** `MultiQuery` logs the
   root as `<Root>.criteria` and each navigation's query as
   `<Target>.criteria` (a many-to-many: the link entity's, then the
   target's); `SubSelect` is one statement per navigation, logged the same
   way; only join mode has its own name, `<Root>.include`, one line.
8. **`Refresh` logs as `<Entity>.get`**: it is the key read.
9. **Tooling without a session may carry its sink another way.** Where the
   migration runner, the shadow, or force-sync take a connection rather than
   a session (Go), the sink reaches them through the language's own carrier
   (Go: the context, `migrations.WithStatementLog`); the reference and PHP
   read it off the session. The lines are the same.
10. **Names are a precedence, outermost first.** A shadow run names the
    runner it drives (`shadow`); SchemaGuard's `MIG-030` read through the
    runner is `migrate` (it *is* the runner's status read); an implementation
    whose names travel by context lets the outer name win.
11. **A one-round-trip realization owns its lines** (Go's `StatementBatcher`,
    PHP's `WriteRun`): only it knows the text it finally sends. A third-party
    realization that does not write them drops them; the contract says it
    must (Go records this in its §10).
12. **Numbers**: a float prints in its shortest round-trip decimal form,
    never with an exponent; an integer as digits; a value type without a
    fixed form (a GUID, a decimal) as its bound form.
13. **A null inside an array parameter is `null`**; a nil byte slice is
    `null`.
14. **The stdout sink writes to the process's standard output stream**, not
    through the language's output buffering (`php://stdout`, not `echo`;
    Go `os.Stdout`); the CLI's `--log` writes to the output the CLI was given,
    which is standard output when the CLI runs as documented.
15. **A failing sink fails the call** in every language; a Go panic in the
    sink is not recovered.
16. **Multi-line SQL is logged on several lines.** The line is "one line per
    statement" by count, not by newline: a trigger body or a `create table`
    keeps its line breaks. A consumer that needs one physical line per
    statement replaces them itself.

## Resilience (Level 4, ADR-0047)

Two things a network engine needs and a file database does not: a bound on
how long one statement may run, and another attempt at opening the
connection when the server is briefly unreachable. Nothing else — no
statement is ever retried, there is no backoff, no circuit breaker, and no
SimpleOrm error code: both failures are the driver's.

**The options**, fixed at open like the others:

- `CommandTimeout`: a duration, or **unset** (the default). Unset, every
  command keeps the driver's own default timeout, untouched — the Level 0–3
  behaviour exactly (SqlClient and Npgsql: 30 seconds; Microsoft.Data.Sqlite:
  30, which SQLite only applies to waiting on a lock). Set, **every command
  the session creates** carries it, in whole seconds **rounded up, at least
  1** (0.2 s → 1, 1.5 s → 2; a zero duration is 1, never the drivers'
  "no limit"); a negative duration refuses at open. A multi-statement message
  (ADR-0044: a driver batch, a multi-statement text) carries the same value
  as one command does. In C# `TimeSpan?`, PHP `commandTimeout` (seconds as a
  float or int, null unset), Go `CommandTimeout time.Duration` — zero is
  Go's spelling of unset (the language has no optional duration), so only
  there a zero means "the driver's default"; a positive sub-second value
  still becomes 1 second. Where the driver has no per-command timeout
  property, the implementation uses the driver's own equivalent for the one
  statement and reports whatever it raises: Go's `database/sql` takes the
  statement's context deadline (`context.WithTimeout` around the call —
  the error is the driver's, wrapping `context.DeadlineExceeded`); PHP PDO
  takes the driver attribute (`PDO::SQLSRV_ATTR_QUERY_TIMEOUT` on
  `pdo_sqlsrv`, `SQL_ATTR_QUERY_TIMEOUT` through `pdo_odbc`, the
  `statement_timeout` setting on `pdo_pgsql` — SQLSTATE `57014`).
- `OpenRetries`: how many **more** attempts the open may make after a failure
  the dialect calls transient; `0` by default — one attempt, the Level 0–3
  behaviour. Negative refuses at open.
- `OpenRetryDelay`: the **fixed** wait between two attempts; **1 second** by
  default. Zero is legal (no wait); negative refuses at open. In C#
  `TimeSpan`, PHP `openRetryDelay` (seconds, float or int), Go
  `OpenRetryDelay time.Duration`; where a zero value cannot mean "unset"
  (Go), zero is the default 1 second, as `MaxStatementsPerMessage`'s zero is
  100 there, and the port names its spelling of "no wait" in its §10.

**"Every command the session creates"** means every statement the session
sends on its connection: registry queries and commands, statement entities,
key reads, generated writes (immediate, flushed, batched), criteria and
loading queries, join includes, select-all, metadata DDL, the migration
runner's statements (the version table, the history, the steps, the guards'
definition reads, the rollbacks), SchemaGuard's describe calls and
introspection, the shadow's replay and introspection (the shadow takes the
timeout for the throwaway session it opens), and the force sync. Each
implementation routes command creation through **one** place that applies
the option, so no path can miss it. Not covered — they run on statements the
**dialect** issues for itself, and keep the driver's default: the migration
run lock (it carries its own 60-second lock timeout on SQL Server and
PostgreSQL, dialects.md), the shadow host's provisioning on the admin
connection, and a dialect's own catalog read-back of a trigger in the
shadow.

**A timeout that fires** surfaces exactly as any other driver failure on
that path: the driver's error, unwrapped, from a session call (Npgsql: an
`NpgsqlException` whose cause is a timeout; SqlClient: a `SqlException` with
number `-2`); `MIG-021` naming the statement from the migration runner, as
every failing migration statement; a `VAL-001` row from SchemaGuard, as every
statement the engine refuses to describe. The session does not roll back,
retry, or close anything because of it. What the connection is good for
afterwards is the driver's answer — Npgsql and SqlClient cancel the
statement on the server and the session's next statement runs (the live
tests check it outside a transaction; on PostgreSQL it has run, the SQL
Server leg awaits an engine); inside a transaction, PostgreSQL has
aborted it and only a rollback is accepted.

**The connect timeout is not one of these options.** How long one open
attempt may take stays where every driver already puts it — in the
connection string (`Connect Timeout` for SqlClient, `Timeout` for Npgsql;
Go's `dial timeout` / `connect_timeout`; PHP's `LoginTimeout` /
`connect_timeout`). The retry multiplies it: the worst case of an open is
`(OpenRetries + 1) × connect timeout + OpenRetries × OpenRetryDelay`.

**The open, as an algorithm** (`Db.OpenAsync(connectionString, options, ct)`):

1. Validate the options (`MaxStatementsPerMessage ≥ 1`, `OpenRetries ≥ 0`,
   `OpenRetryDelay ≥ 0`, `CommandTimeout` unset or ≥ 0); a violation refuses
   before any attempt, with the platform's argument error.
2. For `attempt = 0, 1, 2, …`:
   1. Ask the dialect for a fresh, unopened connection.
   2. Open it, honouring the cancellation token. On success, the session
      owns it: return.
   3. On failure, release the connection. Then **retry only when all of**:
      `attempt < OpenRetries`; the token is not cancelled; the failure is not
      the platform's cancellation signal; and the dialect's `IsTransient`
      answers true for it (dialects.md). Otherwise **rethrow the failure as
      it was raised** — unwrapped, the same object (the last attempt's
      failure, when retries were spent).
   4. Wait `OpenRetryDelay`, honouring the token: a cancellation during the
      wait ends the open with the platform's cancellation signal and no
      further attempt.

Only the **connection open** is ever retried. A statement never is — not on
a timeout, not on a lost connection: the session cannot know whether a
statement that failed in flight was applied, and retrying a write could apply
it twice (§2, no hidden round trips). PostgreSQL's stale-cached-plan retry in
Go (ADR-0040 add.1, dialects.md) is a different rule and stays exactly as it
is. A session whose connection is lost mid-life is not reopened; the
application opens a new session.

**Nothing is logged** by either option: the statement log (above) is for
statements, and an open attempt sends none; a timed-out statement was logged
as it was sent, like every other.

## The rest of the session

Key reads, generated CRUD, criteria queries, and metadata DDL live on the same
session and obey the same three rules — one connection, automatic transaction
enlistment, async with cancellation. Their behaviors carry their own codes
(`CRUD-*`, `DDL-*`, `QRY-005/006/007`) in errors.md.

## Conformance

`conformance/cases/*.json` (format: mapping-rules.md) pins the mapping side of
this surface — result-shape strictness and value encoding (`MAP-*`, `VAL-*`
expectations); `conformance/ast/` pins criteria parameter binding order and
values. The parameter strictness codes (`PRM-001/002`) and single-row codes
(`QRY-001/002`) have **no data-driven form yet**: args are native typed values,
so a portable case format needs an args-shape encoding — reserved, the way
`ast/` was before ADR-0020. Until then each implementation proves them in its own
tests (here: DbParameterTests, DbQueryTests). Lifetime and transaction semantics
likewise have no data-driven form.

## Tracking — the identity map and change snapshots (Level 3 M1, ADR-0034 add.1)

Tracking is a **session mode chosen at open**; the default is **untracked**,
which is the Level 0–2 session exactly: every read materializes a fresh
instance, `Update` writes the full row, and nothing below applies.

```csharp
var options = new DbOptions { Dialect = new SqliteDialect(), Tracking = TrackingMode.Tracked };
```

A tracked session keeps, for its lifetime:

- **One instance per (entity type, key)** — the §7.4 identity, compared
  value-wise (decimals numerically, temporals by instant, byte arrays by
  content). Every read path that materializes an entity consults the map:
  registry queries and streams whose result type is an entity, key reads,
  criteria queries, explicit and batch loading, eager loading in every fetch
  mode (join segments included), and `QueryAll`. Plain result types (DTOs,
  scalars), keyless entities (views without a key, statements), and rows
  whose key has a null part are never tracked.
- **A snapshot of each tracked instance's mapped values** as read or last
  written; owned members snapshot flat, like their columns (ADR-0030).

**The tracked instance wins** (the owner's ruling): a read that finds its key in the
map returns the tracked instance and **does not apply the row's values** —
dirty or not. Loaded navigations stay as they were. `RefreshAsync(entity)`
is the explicit overwrite: it re-reads the row by key, applies every mapped
value onto the instance, and resets the snapshot; a missing row is
`CRUD-001`. `Refresh` works in both modes.

**Questions** — `IsTracked(entity)` (never throws); `ChangesOf(entity)`,
the mapped property names whose value differs from the snapshot (dotted for
owned members), and `IsDirty(entity)`; both are `UOW-001` for an instance
the session does not track, including every instance in an untracked
session.

**Writes keep the map honest:**

- `Insert` registers the instance with its generated key and a fresh
  snapshot.
- `Update` on a **tracked** instance writes **only the dirty columns**
  through the column-list path (ADR-0028) — the version bump and check
  apply as always — and **writes nothing at all when the instance is
  clean** (no statement, no version bump). On an instance the session does
  not know (user-constructed, never inserted here) it writes the full row as
  at Level 1, then tracks it. `UpdateOnly` writes the listed columns as
  always; both refresh the snapshot afterwards.
- `Delete` forgets the key; the deleted instance is no longer tracked.
- A write from an instance that is **not the session's instance for its
  key** — another tracked instance holds the key, or a tracked instance's
  key was changed — is `UOW-002` before any SQL. A key is identity; it never
  updates.

Nothing here adds a round trip: tracking only decides which instance a read
returns and which columns an update writes. The unit of work — scheduling
and a single ordered flush, with declared cascades — builds on it in
unit-of-work.md (M2), detach and merge beside it (M3); the declared hooks —
audit columns and soft delete, written with `DbOptions.Clock` — are in
hooks.md (M3).

### Conformance: `tracking-cases/`

Each case runs against the seeded fixture database in the declared mode and
replays a step script against named instances:

```json
{ "name": "…", "comment": "…", "tracking": "tracked",
  "steps": [
    { "get":    { "entity": "User", "key": 1, "as": "a" } },
    { "set":    { "ref": "a", "property": "Name", "value": "Ada Lovelace" } },
    { "expect": { "dirty": { "ref": "a", "properties": ["Name"] } } },
    { "update": { "ref": "a", "expectWritten": ["Name"] } },
    { "get":    { "entity": "User", "key": 1, "as": "b" } },
    { "expect": { "same": ["a", "b"] } } ] }
```

Steps: `get` (by `key`, a list for composite keys, or `keyOf` another
ref), `new` (a user-constructed instance from `values`), `set`, `insert`,
`update` (with `expectWritten`: the property list the column-list update
was asked to write, `[]` for no statement, `"all"` for a full-row update —
observed through the dialect seam, where every implementation renders
SQL), `updateOnly`, `delete` (by `ref` or by `entity` + `key`), `refresh`,
`load` (a navigation of a ref). Expectations: `same`/`notSame` (reference
identity of two refs; a ref may hop one navigation, `t.User`, or index a
loaded collection, `a.Transactions[0]`), `dirty`, `value` (`ref`,
`property`, `equals`), `tracked` (`ref`, `is`). Any step may carry
`expectError` with the code it must throw. Property names are spelled as
the reference declares them; a port maps them through its own declaration
convention in the runner, as the other runners do.

**Clarifications** (the PHP and Go M1 ports asked; ADR-0035):

- **The snapshot holds the database-bound form of each value** — the type
  converter's output — so "dirty" means "the database would receive a
  different value": a handler-typed or JSON member compares by what would
  be written, an in-place mutation of such a member counts as a change,
  byte arrays compare by content, and two decimals equal in value are clean
  whatever their text. (The reference snapshotted raw values until
  ADR-0035; a JSON list mutated in place read as clean.)
- **`ChangesOf` lists properties in metadata order** (the `EntityMap`
  property order); runners may still compare as sets.
- **A column-list update moves the snapshot forward for the written
  columns and the version only**; an unwritten change stays dirty. A
  column-list update of an instance the session does not know does not
  register it — the session saw only part of the row. A full-row update
  or an insert registers with a snapshot of every column. (The reference
  re-snapshotted the whole row after `UpdateOnly` until ADR-0035, which
  silently cleared an unwritten change.)
- **Every write and the refresh pass the identity gate**: `Insert` (a
  caller-supplied key held by another instance — a generated key has
  nothing to check yet), `Update`, `UpdateOnly`, the entity form of
  `Delete`, and `Refresh` are `UOW-002` for an instance that is not the
  session's instance for its key. The by-key form of `Delete` has no
  instance to gate and forgets whichever instance holds the key.
  `Refresh` of an instance the session does not know applies the row but
  does not register it. (Delete and Refresh were ungated in the reference
  until ADR-0035.)
- **A re-read of a tracked key still maps the row** before discarding it,
  so the row's conversion and strictness errors (`MAP-*`, `VAL-020`)
  still fire; only the values are not applied.
- **A key reused after an external delete**: the instance that writes the
  row becomes the session's instance; the stale holder is forgotten (no
  error after a committed write).
- **Only key, version, or generated values differing** is clean: no
  statement, no snapshot change — those columns are never in a SET; a
  changed key is `UOW-002`.
- **Which types are tracked**: every keyed entity map, views included
  (writes against a view still refuse `CRUD-003`); plain result types and
  types with no mapping metadata are never tracked.
- **Transactions do not touch the map**: a rollback reverts the database,
  not the instances or the snapshots — `Refresh` is the way back.
- **Navigations are not dirty state** and are never part of a snapshot. A
  re-read leaves a tracked instance's navigations as they were; an
  explicit load or an include always (re)assigns the navigation it names,
  on the tracked instance.
- **Tracking is per session**: two sessions have two maps; closing a
  session clears its map.
- **Conformance spelling**: `tracking-cases` name properties as the
  reference declares them (`Name`, `Address.City`), unlike `crud-cases`,
  which name columns; a port's runner maps through its own declaration
  convention. A single-part `key` is a bare scalar; `keyOf` yields the
  key of another ref.
- **Every materialized keyed entity row is tracked**, the many-to-many
  link rows a load reads included, even though the caller never receives
  them: one rule for every read path, no special cases.
- **Loading correlates by the live values** of a tracked instance's key and
  foreign-key properties — a local edit of a tracked FK changes which owner
  a later load attaches the target to; the snapshot plays no part in
  loading.
- **Order of refusals on a column-list update**: the list validates first
  (`CRUD-005`/`006`/`007`), then navigation consistency (`CRUD-004`), then
  identity (`UOW-002`) — all before any SQL.
- **A read that fails midway** leaves the rows it already resolved
  registered; the map is a cache of what the session materialized, not a
  transaction.
- **Metadata order** means the `EntityMap` property order: a type's own
  mapped properties first, inherited ones after (mapping-rules.md), owned
  members at their navigation's position.
- **Value-shaped results.** Where a language materializes values rather
  than references (Go `[]T` beside `[]*T`), only the reference-shaped
  result is the tracked instance; a value-shaped result for a known key is
  a copy of the tracked state and is never registered. The port records
  the shape in its §10 table; the spec's "instance" means the
  reference-shaped one.
- **A full-row `Update` validates like a full row** before anything else:
  every navigation's consistency (`CRUD-004`) is checked first, then the
  identity gate (`UOW-002`), then the snapshot decides what to write — so
  a clean tracked instance carrying an inconsistent navigation is refused,
  not silently skipped. The column-list update checks only the navigations
  of the listed properties, after the list validates.

### The step scripts — grammar, values, the fixture, the seam (Level 3 exit, ADR-0039)

The four Level 3 folders — `tracking-cases/`, `flush-cases/`, `hook-cases/`,
`merge-cases/` — and Level 4's `batch-cases/` (ADR-0044) share one step-script
form. This section is normative for all of them; the examples above and in unit-of-work.md illustrate it. (The
Level 3 exit audit — a design for a Python port written from `spec/` and
`conformance/` alone — found the form defined only by example.)

**Grammar.** A case is `{ "name", "comment", "tracking": "tracked" |
"untracked", "batching"?, "steps": [ … ] }`. `batching` opens the session
with the batching option ("Batching" above): `{ "mode": "auto" | "off",
"maxStatementsPerMessage"? }`; absent, the session is `Off`. Every step is an object with exactly one verb
key and, optionally, `"expectError": "<code>"`. The verbs and their fields:

| verb | fields | meaning |
|---|---|---|
| `get` | `entity`, `key`, `as` | read by key; binds the ref (a composite key is an array of its parts in key order, ADR-0044) |
| `new` | `entity`, `as`, `values`?, `navigations`? | construct in user code — `values` sets properties, `navigations` sets a navigation to a ref or a collection to a list of refs; the database is not touched |
| `set` | `ref`, `property`, `value` | assign a property |
| `insert`, `refresh`, `persist`, `remove`, `detach` | `ref` | the operation of that name on the instance |
| `update` | `ref`, `expectWritten`? | the full-row update (a tracked instance's dirty update); `expectWritten` is the property list the column-list rendering was asked to write, `"all"` for the full-row rendering, `[]` for no statement |
| `updateOnly` | `ref`, `properties`, `expectWritten`? | the column-list update |
| `delete` | `ref` (the entity form) or `entity` + `key` (the key form) | |
| `load` | `ref`, `navigation` | explicit loading of one navigation |
| `flush` | `expectStatements`?, `expectSummary`?, `expectMessages`? | unit-of-work.md "Conformance" |
| `batch` | `ops`, `as`?, `expectStatements`?, `expectMessages`?, `expectAffected`? | queue every op on one explicit batch, then send it ("The explicit batch"); each op is an object with one key — `insert`, `update`, `updateOnly`, `delete` (fields as the steps of those names), or `execute` (`command` — a fixture command by name — and `args`); `expectAffected` is the result's per-operation counts; `as` keeps the spent batch under a name |
| `batchAgain` | `ref`, `op`? | use a kept batch again: queue `op` on it, or, without `op`, send it — both `TX-003` |
| `merge` | `ref`, `as` | binds the instance merge returns |
| `begin`, `commit`, `rollback` | `{}` | the session transaction |
| `expect` | one expectation kind | below |

Expectation kinds, one per `expect`: `same` / `notSame` (two paths — instance
identity), `dirty` (`ref`, `properties`), `value` (`ref`, `property`,
`equals`), `tracked` (`ref`, `is`), `pending` (a bool — `HasPendingWork`),
`sameValue` (two paths — value equality), `count` (`entity`, `equals` —
counted through a second connection, so uncommitted work is invisible to it).
A **path** is a ref, `ref.Property`, or `ref.Collection[i]`, accepted wherever
a ref is resolved. `as` binds a name and may rebind it. A step carrying
`expectError` must fail with that code — for an `expect` step, evaluating the
expectation is what fails (`dirty` of an untracked instance is `UOW-001`) —
and binds nothing; the case then continues.

**Values.** Every `value`, every entry of `values`, and every `equals` uses
the conformance value encoding of mapping-rules.md (a decimal as a string, an
instant as ISO-8601 with seven fractional digits and `Z`, an enum by name,
bytes as base64, keys as bare scalars). A runner decodes through the
property's declared type when it sets and encodes the property's value the
same way when it compares, so comparison is by value — a decimal numerically,
an instant by its moment, a bool strictly, never a text comparison of a
language's own formatting. Property lists (`dirty.properties`,
`expectWritten`, a statement's `columns`) may be compared as sets. A `value` expectation may name an owned navigation (`Address`) instead of a
member: `equals` is then `null` (the navigation is null) or `{}` (an owned object
exists; members are checked one by one).

**Messages.** `expectMessages` is the plan's grouping of the step's
statements ("Batching" above): one entry per message, in order, each the
number of statements it carried — registry commands included, although the
recorder below lists generated writes only. It is the plan — formed under the
runner's dialect, SQLite — whether or not the realization sent each message as
one round trip.

**The fixture command** for `execute`: `SetDisplayName`, the registry command
`update users set display_name = @DisplayName where id = @Id`, args `Id`
(int64) and `DisplayName` (string, nullable), declared natively in every
runner.

**The clock** is fixed at `2026-08-29T00:00:00Z` for every Level 3 runner,
not only `hook-cases` — the audit columns appear in every folder's write
lists.

**The fixture, as the scripts spell it.** Property and navigation names are
the reference's; a port declares the same entities natively and its runner
maps these names to its own — nothing else needs them (the entity export
stays column-only). Every table entity inherits `CreatedAtUtc` → `created_at`
(`[CreatedAt]`) and `UpdatedAtUtc` → `updated_at` (`[UpdatedAt]`, nullable)
from a base class, exported last:

| entity (table) | properties → columns | navigations |
|---|---|---|
| `User` (`users`) | `Id` → `id` (key, generated), `Name` → `name`, `Email` → `email`, `DisplayName` → `display_name` | `Transactions` one-to-many (`Transaction.UserId`), `Roles` many-to-many via `UserRole`, `Profile` one-to-one (`UserProfile.UserId`) |
| `Role` (`roles`) | `Id`, `Name` → `role_name` (renamed from `name`), `IsDeleted` → `is_deleted` (soft delete) | `Users` many-to-many via `UserRole` |
| `UserRole` (`user_roles`) | `UserId` → `user_id` (key), `RoleId` → `role_id` (key), `GrantedBy` → `granted_by` | `User`, `Role` many-to-one |
| `UserProfile` (`user_profiles`) | `Id`, `UserId` → `user_id`, `Bio` → `bio`, `AvatarUrl` → `avatar_url`, `Avatar` → `avatar` (nullable `bytes`, V0012, ADR-0040 add.1), owned `Address` → `Address.Street` → `address_street`, `Address.City` → `address_city`, `Address.PostalCode` → `address_postal_code` | `User` many-to-one |
| `Transaction` (`transactions`) | `Id`, `UserId` → `user_id`, `Status` → `status` (enum by name), `Amount` → `amount`, `Version` → `version` (version), `Note` → `note` | `User` many-to-one, `Details` one-to-many (`TransactionDetail.TransactionId`), cascade persist + remove |
| `TransactionDetail` (`transaction_details`) | `Id`, `TransactionId` → `transaction_id`, `Description` → `description`, `Quantity` → `quantity`, `UnitPrice` → `unit_price` | `Transaction` many-to-one |

**The database** is built from the fixture's metadata (the generated `CREATE
TABLE`s) and `fixtures/seed.json` before each case. It carries **no
foreign-key constraints**, and the session enables no enforcement: constraints
are the application's schema, and the library never adds any — the write
order the unit of work promises is what keeps a constrained database happy,
not what the fixture checks. (A `schema/sqlite/` migrations directory named
by an early README was never created; the metadata is the fixture's schema.)

**The seam.** `expectWritten` and `expectStatements` observe the dialect: the
runner wraps the dialect's four **write renderings** — insert, full-row
update, column-list update, delete — and records one `{ "op", "entity",
"columns"? }` per rendering the session asks for, in order. `op` is
`"insert"`, `"update"`, or `"delete"`; `entity` is the class name as the
fixture spells it; the full-row update carries `"columns": "all"`, the
column-list update the property list it was asked to write, in list order. A
soft delete is the column-list update it is. Reads, statement execution, and
failures are not recorded; the buffer is cleared before each step; a step's
`expectWritten` is the columns of its last recorded update (`[]` when the step
recorded none). **The message boundary** is the session's, not the dialect's
(no seam member sees a message): each implementation gives its runner a way to
observe the size of every message a flush or a batch send formed — a test hook,
never public API — and `expectMessages` compares those sizes in order. Every
runner runs every `flush-cases` file **twice**, `Off` and `Auto` (with the
default `MaxStatementsPerMessage`): the statements, the summary, and every
expectation must come out the same in both modes. `flush-cases` therefore carry
neither `batching` nor `expectMessages`; the grouping is `batch-cases`'.
