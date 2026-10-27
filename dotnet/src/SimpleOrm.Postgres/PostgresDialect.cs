using System.Data.Common;
using System.Text;
using System.Text.RegularExpressions;
using Npgsql;

namespace SimpleOrm.Postgres;

/// <summary>
/// PostgreSQL implementation of <see cref="IDialect"/> backed by Npgsql
/// (ADR-0025, the second Level 4 dialect — the one several Level 1 seams were
/// designed for). The promises come due here: <c>SupportsArrayParameters</c>
/// is realized (<c>= any(@ids)</c>, one typed-array parameter, SQL untouched),
/// the migration run lock evolves to an advisory lock
/// (<c>pg_advisory_xact_lock</c> inside one transaction — Postgres DDL is
/// transactional, so the SQLite atomicity carries over without per-migration
/// transactions), materialized views come alive
/// (<c>SupportsMaterializedViews</c>), and <c>RETURNING</c> works exactly as on
/// SQLite. Identifiers are always double-quoted: names are snake_case lowercase
/// by convention, so quoting matches the catalog's fold while staying
/// reserved-word-proof (<c>user</c>, <c>order</c>, <c>desc</c>). Temporals bind
/// natively (<see cref="BindsTemporalsNatively"/>): Postgres refuses
/// <c>timestamptz >= text</c>, and <c>timestamptz</c> is the marker-carrying
/// storage — it reads back Kind=Utc, while a <c>timestamp without time zone</c>
/// reads back Kind=Unspecified and refuses with <c>VAL-020</c> (an
/// ITypeHandler declaring the column's kind is the legacy escape, as on SQL
/// Server).
/// </summary>
public sealed class PostgresDialect : IDialect
{
    public string Name => "postgres";

    public DbConnection CreateConnection(string connectionString)
        => new NpgsqlConnection(connectionString);

    /// <summary>Always double-quoted (ADR-0025): lowercase names match the catalog's fold, reserved words stop mattering.</summary>
    public string QuoteIdentifier(string identifier)
        => "\"" + identifier.Replace("\"", "\"\"") + "\"";

    public string LimitOffsetClause(string? limitParameter, string? offsetParameter) => (limitParameter, offsetParameter) switch
    {
        (not null, not null) => $"limit {limitParameter} offset {offsetParameter}",
        (not null, null) => $"limit {limitParameter}",
        (null, not null) => $"offset {offsetParameter}",   // no SQLite-style limit -1 workaround needed
        _ => string.Empty,
    };

    public string SelectSql(SelectAst select, BindCriteriaParameter bindParameter)
        => AnsiSelectRenderer.SelectSql(this, select, bindParameter);

    public bool SupportsArrayParameters => true;   // §7.12 realized: one typed-array parameter, registry SQL says = any(@ids)

    public bool BindsTemporalsNatively => true;   // timestamptz >= text refuses; the provider gets UTC CLR values

    public bool PagingRequiresOrderBy => false;

    public bool SupportsRowValueIn => true;   // (a, b) in (select …) parses natively

    public bool SupportsMaterializedViews => true;

    public bool SupportsProcedures => true;

    public bool SupportsTransactionalDdl => true;

    // The introspection result columns carry the spec's names on every non-SQLite dialect
    // (spec/dialects.md "Introspection result shapes"), so a consumer may read by name or by
    // ordinal; the reference reads by ordinal. `pk` is a presence flag here, not an ordinal.
    public string ColumnsInfoSql
        => """
           select a.attname as name,
                  format_type(a.atttypid, a.atttypmod) as type,
                  (case when a.attnotnull then 1 else 0 end)::bigint as "notnull",
                  (case when exists (
                      select 1 from pg_index i
                      where i.indrelid = a.attrelid and i.indisprimary and a.attnum = any(i.indkey)
                  ) then 1 else 0 end)::bigint as pk,
                  (case when a.attidentity in ('a', 'd') then 1 else 0 end)::bigint as generated
           from pg_attribute a
           where a.attrelid = to_regclass(@relation) and a.attnum > 0 and not a.attisdropped
           order by a.attnum
           """;

    /// <summary><c>pg_get_viewdef</c> reformats the parse tree, never the submitted text: the guard round-trips through a probe view (ADR-0043).</summary>
    public bool ViewDefinitionIsVerbatim => false;

    public string ViewDefinitionSql
        => "select pg_get_viewdef(c.oid, true) as definition from pg_class c "
            + "where c.oid = to_regclass(@relation) and c.relkind in ('v', 'm')";

    public string IndexesInfoSql
        => """
           select ic.relname as index_name,
                  (case when i.indisunique then 1 else 0 end)::bigint as "unique",
                  k.n::bigint as seqno,
                  a.attname as "column",
                  (case when (i.indoption[k.n] & 1) <> 0 then 1 else 0 end)::bigint as "desc"
           from pg_index i
           join pg_class ic on ic.oid = i.indexrelid
           cross join lateral generate_series(0, i.indnkeyatts - 1) as k(n)
           join pg_attribute a on a.attrelid = i.indrelid and a.attnum = i.indkey[k.n]
           where i.indrelid = to_regclass(@relation)
             and not i.indisprimary
             and not exists (select 1 from pg_constraint con where con.conindid = i.indexrelid)
           order by ic.relname, k.n
           """;

    public bool IsDeclaredTypeCompatible(string declaredType, Type clrType, bool enumAsInt)
    {
        var type = Nullable.GetUnderlyingType(clrType) ?? clrType;
        var declared = BaseTypeName(declaredType);
        if (declared.Length == 0)
        {
            return false;
        }

        if (type.IsEnum)
        {
            return enumAsInt
                ? declared is "integer" or "int4" or "bigint" or "int8" or "smallint" or "int2"
                : declared is "text" or "character varying" or "varchar" or "character" or "char";
        }

        return declared switch
        {
            "integer" or "int4" => type == typeof(int) || type == typeof(long) || type == typeof(short),
            "bigint" or "int8" => type == typeof(long),
            "smallint" or "int2" => type == typeof(short) || type == typeof(int) || type == typeof(long),
            "boolean" or "bool" => type == typeof(bool),
            "numeric" or "decimal" or "money" => type == typeof(decimal),
            "double precision" or "float8" => type == typeof(double),
            "real" or "float4" => type == typeof(float),
            "text" or "character varying" or "varchar" or "character" or "char" or "name" => type == typeof(string),
            "bytea" => type == typeof(byte[]),
            "uuid" => type == typeof(Guid),
            // Markerless storage is still *type*-compatible with DateTime; the
            // VAL-020 kind rule applies per value at read time (as on SQL Server).
            "timestamp with time zone" or "timestamptz" => type == typeof(DateTime) || type == typeof(DateTimeOffset),
            "timestamp without time zone" or "timestamp" => type == typeof(DateTime),
            "date" => type == typeof(DateTime) || type.FullName == "System.DateOnly",
            "time without time zone" or "time" => type == typeof(TimeSpan) || type.FullName == "System.TimeOnly",
            _ => false,
        };
    }

    /// <summary>
    /// The run lock (§7.23, evolved as anticipated): one transaction for the whole
    /// run holding an exclusive advisory lock scoped to it — released on
    /// commit/rollback, never leaked. DDL is transactional on Postgres, so a
    /// failed run still applies nothing; the anticipated per-migration
    /// transactions turned out unnecessary (ADR-0025).
    /// </summary>
    public DbTransaction BeginMigrationRunLock(DbConnection connection)
    {
        var transaction = connection.BeginTransaction();
        try
        {
            using var command = connection.CreateCommand();
            command.Transaction = transaction;
            // The key pair is arbitrary but fixed: ASCII 'Simp', 'leOr'.
            command.CommandText =
                "set local lock_timeout = '60s'; "
                + "select pg_advisory_xact_lock(1399418224, 1818578802)";
            command.ExecuteNonQuery();
            return transaction;
        }
        catch
        {
            transaction.Dispose();
            throw;
        }
    }

    public string VersionTableSql
        => """
           create table if not exists schema_version (
               version      bigint not null,
               object       text not null,
               description  text not null,
               checksum     text not null,
               applied_at   text not null,
               execution_ms bigint not null,
               primary key (version, object)
           )
           """;

    public string RenameTableSql(string fromName, string toName)
        => $"alter table {QuoteIdentifier(fromName)} rename to {QuoteIdentifier(toName)}";

    public string RenameColumnSql(string table, string fromName, string toName)
        => $"alter table {QuoteIdentifier(table)} rename column {QuoteIdentifier(fromName)} to {QuoteIdentifier(toName)}";

    public string AddColumnSql(string table, string column, string storageType, bool nullable, string? defaultSql)
        => $"alter table {QuoteIdentifier(table)} add column {QuoteIdentifier(column)} {storageType}"
            + (nullable ? string.Empty : " not null")
            + (defaultSql is null ? string.Empty : " default " + defaultSql);

    public string DropColumnSql(string table, string column)
        => $"alter table {QuoteIdentifier(table)} drop column {QuoteIdentifier(column)}";

    public string DropTableSql(string table)
        => "drop table " + QuoteIdentifier(table);

    public string DropIndexSql(string table, string index)
        => "drop index " + QuoteIdentifier(index);   // index names are schema-scoped, not table-scoped

    // --- triggers (ADR-0042) ---------------------------------------------------------

    /// <summary>
    /// A PostgreSQL trigger executes a function: the dialect renders one per
    /// trigger, named <c>&lt;trigger&gt;_fn</c>, created with it and dropped with
    /// it, never in the metadata. Row-level (<c>for each row</c>), so the body's
    /// rows are one-row derived tables over <c>new</c>/<c>old</c>; the function
    /// returns <c>null</c> after an AFTER trigger and the row otherwise (<c>old</c>
    /// on delete, <c>new</c> else), which the dialect appends — never the author.
    /// INSTEAD OF exists only on views (<c>DDL-005</c> on a table).
    /// </summary>
    public IReadOnlyList<string> CreateTriggerSql(string relation, bool onView, TriggerDefinition trigger)
    {
        if (trigger.Timing == TriggerTiming.InsteadOf && !onView)
        {
            throw new SimpleOrmException("DDL-005", trigger.Name, "PostgreSQL has INSTEAD OF triggers on views only; declare Before or After on a table");
        }

        var body = TriggerTokens.Render(trigger.Sql, segment => segment.Kind switch
        {
            "new" => TriggerTokens.RowSelect("new", segment.Columns, QuoteIdentifier),
            "old" => TriggerTokens.RowSelect("old", segment.Columns, QuoteIdentifier),
            _ => "now()",
        });
        var returns = trigger.Timing == TriggerTiming.After
            ? "    return null;"
            : "    if tg_op = 'DELETE' then return old; end if;\n    return new;";
        var function = FunctionName(trigger.Name);
        return
        [
            "create or replace function " + QuoteIdentifier(function) + "() returns trigger language plpgsql as $$\nbegin\n"
                + body.Trim() + "\n" + returns + "\nend\n$$",
            "create trigger " + QuoteIdentifier(trigger.Name) + " " + Timing(trigger.Timing) + " "
                + string.Join(" or ", trigger.EventList().Select(e => EventWord(e)
                    + (e == TriggerEvent.Update && trigger.UpdateOf.Count > 0 ? " of " + string.Join(", ", trigger.UpdateOf.Select(QuoteIdentifier)) : string.Empty)))
                + " on " + QuoteIdentifier(relation) + " for each row execute function " + QuoteIdentifier(function) + "()",
        ];
    }

    public IReadOnlyList<string> DropTriggerSql(string relation, string triggerName)
        =>
        [
            "drop trigger if exists " + QuoteIdentifier(triggerName) + " on " + QuoteIdentifier(relation),
            "drop function if exists " + QuoteIdentifier(FunctionName(triggerName)) + "()",
        ];

    /// <summary>
    /// The function reconstructed from <c>pg_proc.prosrc</c> (the body verbatim)
    /// and the trigger from <c>pg_trigger</c> (<c>tgtype</c> bits, <c>tgattr</c>
    /// columns), in the dialect's own spelling — so each row equals the matching
    /// <see cref="CreateTriggerSql"/> statement once whitespace is collapsed.
    /// </summary>
    public string TriggerDefinitionSql
        => """
           select 'create or replace function "' || p.proname || '"() returns trigger language plpgsql as $$' || p.prosrc || '$$' as definition
           from pg_trigger t
           join pg_proc p on p.oid = t.tgfoid
           where t.tgname = @relation and not t.tgisinternal
           union all
           select 'create trigger "' || t.tgname || '" '
               || case when (t.tgtype & 2) <> 0 then 'before' when (t.tgtype & 64) <> 0 then 'instead of' else 'after' end
               || ' '
               || array_to_string(array_remove(array[
                      case when (t.tgtype & 4) <> 0 then 'insert' end,
                      case when (t.tgtype & 16) <> 0 then 'update' || coalesce((
                          select ' of ' || string_agg('"' || a.attname || '"', ', ' order by k.ord)
                          from unnest(t.tgattr::smallint[]) with ordinality as k(attnum, ord)
                          join pg_attribute a on a.attrelid = t.tgrelid and a.attnum = k.attnum), '') end,
                      case when (t.tgtype & 8) <> 0 then 'delete' end], null), ' or ')
               || ' on "' || c.relname || '" for each row execute function "' || p.proname || '"()' as definition
           from pg_trigger t
           join pg_class c on c.oid = t.tgrelid
           join pg_proc p on p.oid = t.tgfoid
           where t.tgname = @relation and not t.tgisinternal
           """;

    private static string FunctionName(string triggerName) => triggerName + "_fn";

    private static string Timing(TriggerTiming timing) => timing switch
    {
        TriggerTiming.Before => "before",
        TriggerTiming.After => "after",
        _ => "instead of",
    };

    private static string EventWord(TriggerEvent e) => e switch
    {
        TriggerEvent.Insert => "insert",
        TriggerEvent.Update => "update",
        _ => "delete",
    };

    public string CreateViewSql(EntityMap map)
        => CreateViewSql(map.RelationName!, map.DefiningSql!, map.Kind == RelationKind.MaterializedView);

    public string CreateViewSql(string view, string body, bool materialized)
        => materialized
            ? "create materialized view if not exists " + QuoteIdentifier(view) + " as\n" + body
            : "create or replace view " + QuoteIdentifier(view) + " as\n" + body;

    public string DropViewSql(string view, bool materialized)
        => (materialized ? "drop materialized view if exists " : "drop view if exists ") + QuoteIdentifier(view);

    /// <summary>A table from its snapshot (ADR-0043): one generated key renders inline as identity; any other key as the trailing constraint.</summary>
    public string CreateTableSql(TableSchema schema)
    {
        var builder = new StringBuilder("create table if not exists ").Append(QuoteIdentifier(schema.Name)).Append(" (");
        var generatedKey = schema.Columns.Count(c => c.Key && c.Generated) == 1 && schema.Columns.Count(c => c.Key) == 1;
        var first = true;
        foreach (var column in schema.Columns)
        {
            builder.Append(first ? "\n    " : ",\n    ").Append(QuoteIdentifier(column.Name)).Append(' ').Append(column.StorageType);
            first = false;
            if (column.Key && generatedKey)
            {
                builder.Append(" generated by default as identity primary key");
                continue;
            }

            if (!column.Nullable)
            {
                builder.Append(" not null");
            }
        }

        if (!generatedKey && schema.Columns.Any(c => c.Key))
        {
            builder.Append(",\n    primary key (")
                .Append(string.Join(", ", schema.Columns.Where(c => c.Key).Select(c => QuoteIdentifier(c.Name))))
                .Append(')');
        }

        return builder.Append("\n)").ToString();
    }

    public string CreateIndexSql(string table, TableSchema.Index index)
        => "create " + (index.Unique ? "unique " : string.Empty) + "index if not exists " + QuoteIdentifier(index.Name)
            + " on " + QuoteIdentifier(table) + " ("
            + string.Join(", ", index.Columns.Select(c => QuoteIdentifier(c.ColumnName) + (c.Descending ? " desc" : string.Empty)))
            + ")";

    public string CreateTableSql(EntityMap map)
    {
        var builder = new StringBuilder("create table if not exists ")
            .Append(QuoteIdentifier(map.RelationName!)).Append(" (");

        var first = true;
        foreach (var property in map.Properties)
        {
            builder.Append(first ? "\n    " : ",\n    ").Append(QuoteIdentifier(property.ColumnName)).Append(' ');
            first = false;

            if (map.KeyStrategy == KeyStrategy.DatabaseGenerated && property.IsKey)
            {
                // "by default" (not ALWAYS) so fixtures may insert explicit keys.
                builder.Append(StorageType(map, property)).Append(" generated by default as identity primary key");
                continue;
            }

            builder.Append(StorageType(map, property));
            if (!property.IsNullable)
            {
                builder.Append(" not null");
            }

            if (map.KeyStrategy == KeyStrategy.ClientGuid && property.IsKey)
            {
                builder.Append(" primary key");
            }
        }

        if (map.KeyStrategy == KeyStrategy.Natural && map.KeyProperties.Count > 0)
        {
            builder.Append(",\n    primary key (")
                .Append(string.Join(", ", map.KeyProperties.Select(k => QuoteIdentifier(k.ColumnName))))
                .Append(')');
        }

        return builder.Append("\n)").ToString();
    }

    public IReadOnlyList<string> CreateIndexSql(EntityMap map)
        => map.Indexes
            .Select(index =>
                "create " + (index.Unique ? "unique " : string.Empty) + "index if not exists "
                + QuoteIdentifier(index.Name)
                + " on " + QuoteIdentifier(map.RelationName!) + " ("
                + string.Join(", ", index.Columns.Select(c => QuoteIdentifier(c.ColumnName) + (c.Descending ? " desc" : string.Empty)))
                + ")")
            .ToArray();

    public string InsertSql(EntityMap map)
    {
        var columns = map.Properties.Where(p => !p.IsGenerated).ToArray();
        var sql = "insert into " + QuoteIdentifier(map.RelationName!)
            + " (" + string.Join(", ", columns.Select(c => QuoteIdentifier(c.ColumnName))) + ")"
            + " values (" + string.Join(", ", columns.Select(c => "@" + c.ColumnName)) + ")";

        if (map.KeyStrategy == KeyStrategy.DatabaseGenerated)
        {
            sql += " returning " + QuoteIdentifier(map.KeyProperties[0].ColumnName);
        }

        return sql;
    }

    public string UpdateSql(EntityMap map)
        => RenderUpdate(map, map.Properties.Where(p => !p.IsKey && !p.IsVersion && !p.IsGenerated));

    public string UpdateOnlySql(EntityMap map, IReadOnlyList<PropertyMap> properties)
        => RenderUpdate(map, properties);

    private string RenderUpdate(EntityMap map, IEnumerable<PropertyMap> set)
    {
        var assignments = set
            .Select(p => QuoteIdentifier(p.ColumnName) + " = @" + p.ColumnName)
            .ToList();
        if (map.VersionProperty is { } version)
        {
            assignments.Add(QuoteIdentifier(version.ColumnName) + " = " + QuoteIdentifier(version.ColumnName) + " + 1");
        }

        return "update " + QuoteIdentifier(map.RelationName!)
            + " set " + string.Join(", ", assignments)
            + " where " + KeyPredicate(map)
            + (map.VersionProperty is { } v ? " and " + QuoteIdentifier(v.ColumnName) + " = @" + v.ColumnName : string.Empty);
    }

    public string DeleteSql(EntityMap map, bool checkVersion)
        => "delete from " + QuoteIdentifier(map.RelationName!)
            + " where " + KeyPredicate(map)
            + (checkVersion && map.VersionProperty is { } v ? " and " + QuoteIdentifier(v.ColumnName) + " = @" + v.ColumnName : string.Empty);

    /// <summary>The command tag's count is the statement's own, triggers or not (ADR-0040 add.3).</summary>
    public bool ReportsAffectedRowsBySelect => false;

    /// <summary>The provider's <c>DbBatch</c> (<c>NpgsqlBatch</c>) on <c>net10.0</c>; none on <c>netstandard2.0</c>, where planned messages run one statement at a time (ADR-0044).</summary>
#if NET
    public bool SupportsStatementBatching => true;
#else
    public bool SupportsStatementBatching => false;
#endif

    /// <inheritdoc />
    public int? MaxParametersPerMessage => null;

    /// <summary>
    /// Whether a failed open is transient (ADR-0047, spec/dialects.md "Transient
    /// open failures"). The engine's answer settles it: when the cause chain holds a
    /// <see cref="PostgresException"/> (the server spoke), its SQLSTATE alone decides
    /// (<see cref="IsTransientSqlState"/>), whatever transport failure the chain also
    /// carries — Npgsql gathers one failure per host tried into an
    /// <see cref="AggregateException"/>, whose every inner failure is walked. Only
    /// without one does a socket, I/O, or timeout failure anywhere in the chain
    /// answer true — Npgsql reports an unreachable or vanished server as an
    /// <see cref="NpgsqlException"/> wrapping one. The driver's <c>IsTransient</c>
    /// flag is not consulted: Npgsql 4.1 (<c>netstandard2.0</c>) and 9.x
    /// (<c>net10.0</c>) list different states, and the two targets answer alike from
    /// the rule written once here.
    /// </summary>
    public bool IsTransient(Exception exception)
    {
        var chain = CauseChain(exception).ToList();
        if (chain.OfType<PostgresException>().FirstOrDefault() is { } postgres)
        {
            return IsTransientSqlState(postgres.SqlState);
        }

        return chain.Any(link => link is System.Net.Sockets.SocketException or System.IO.IOException or TimeoutException);
    }

    /// <summary>The failure and every cause below it, depth first: <c>InnerException</c>, and each of an <see cref="AggregateException"/>'s inner failures.</summary>
    private static IEnumerable<Exception> CauseChain(Exception exception)
    {
        yield return exception;
        var causes = exception is AggregateException aggregate
            ? aggregate.InnerExceptions
            : exception.InnerException is { } inner ? [inner] : (IEnumerable<Exception>)[];
        foreach (var cause in causes)
        {
            foreach (var link in CauseChain(cause))
            {
                yield return link;
            }
        }
    }

    /// <summary>
    /// SQLSTATE class <c>08</c> (connection exception: 08000, 08001, 08003, 08004,
    /// 08006, 08007, 08P01), <c>53300</c> (too_many_connections), and <c>57P03</c>
    /// (cannot_connect_now — the server is starting up or shutting down). An
    /// authentication failure (28xxx) or a missing database (3D000) is not: it
    /// fails the same way again.
    /// </summary>
    public static bool IsTransientSqlState(string? sqlState)
        => sqlState is not null
           && (sqlState.StartsWith("08", StringComparison.Ordinal) || sqlState is "53300" or "57P03");

    private string KeyPredicate(EntityMap map)
        => string.Join(" and ", map.KeyProperties.Select(k => QuoteIdentifier(k.ColumnName) + " = @" + k.ColumnName));

    /// <summary>
    /// CLR → PostgreSQL storage type (ADR-0025, ADR-0043), spelled the way
    /// <c>format_type</c> reports it so snapshots/diff/sync compare cleanly.
    /// <c>numeric</c> is unconstrained unless a <c>Precision</c>/<c>Scale</c> facet
    /// says <c>numeric(p,s)</c>; a string with a <c>Length</c> facet is
    /// <c>character varying(n)</c>, otherwise <c>text</c> (keys need no length
    /// hack — <c>text</c> is indexable); <c>bytea</c> has no length; temporals are
    /// <c>timestamp with time zone</c>, the marker-carrying storage.
    /// </summary>
    public string StorageType(EntityMap map, PropertyMap property)
    {
        var type = Nullable.GetUnderlyingType(property.ClrType) ?? property.ClrType;
        if (type.IsEnum)
        {
            return property.EnumAsInt ? "integer" : "text";
        }

        if (type == typeof(int))
        {
            return "integer";
        }

        if (type == typeof(long))
        {
            return "bigint";
        }

        if (type == typeof(short))
        {
            return "smallint";
        }

        if (type == typeof(bool))
        {
            return "boolean";
        }

        if (type == typeof(decimal))
        {
            return property.Precision is { } precision ? $"numeric({precision},{property.Scale ?? 0})" : "numeric";
        }

        if (type == typeof(double))
        {
            return "double precision";
        }

        if (type == typeof(float))
        {
            return "real";
        }

        if (type == typeof(byte[]))
        {
            return "bytea";
        }

        if (type == typeof(Guid))
        {
            return "uuid";
        }

        if (type == typeof(DateTime) || type == typeof(DateTimeOffset))
        {
            return "timestamp with time zone";
        }

        if (type.FullName == "System.DateOnly")
        {
            return "date";
        }

        if (type == typeof(TimeSpan) || type.FullName == "System.TimeOnly")
        {
            return "time without time zone";
        }

        // string, JSON, handler types.
        return property.Length is { } length && type == typeof(string) ? $"character varying({length})" : "text";
    }

    /// <summary>
    /// The declared type's base name: lowercased, any parenthesized length or
    /// precision removed — including mid-name, as in
    /// <c>timestamp(3) with time zone</c>.
    /// </summary>
    private static string BaseTypeName(string declaredType)
    {
        var declared = Regex.Replace(declaredType, @"\([^)]*\)", string.Empty);
        return Regex.Replace(declared, @"\s+", " ").Trim().ToLowerInvariant();
    }
}
