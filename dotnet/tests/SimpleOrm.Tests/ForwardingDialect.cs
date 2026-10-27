using System.Data.Common;

namespace SimpleOrm.Tests;

/// <summary>A dialect that forwards every seam member to another, each overridable — for tests that change one fact of a real dialect.</summary>
internal abstract class ForwardingDialect(IDialect inner) : IDialect
{
    protected IDialect Inner { get; } = inner;

    public virtual string InsertSql(EntityMap map) => Inner.InsertSql(map);
    public virtual string UpdateSql(EntityMap map) => Inner.UpdateSql(map);
    public virtual string UpdateOnlySql(EntityMap map, IReadOnlyList<PropertyMap> properties) => Inner.UpdateOnlySql(map, properties);
    public virtual string DeleteSql(EntityMap map, bool checkVersion) => Inner.DeleteSql(map, checkVersion);
    public virtual DbConnection CreateConnection(string connectionString) => Inner.CreateConnection(connectionString);
    public virtual string QuoteIdentifier(string identifier) => Inner.QuoteIdentifier(identifier);
    public virtual string CreateTableSql(EntityMap map) => Inner.CreateTableSql(map);
    public virtual IReadOnlyList<string> CreateIndexSql(EntityMap map) => Inner.CreateIndexSql(map);
    public virtual string CreateViewSql(EntityMap map) => Inner.CreateViewSql(map);
    public virtual string LimitOffsetClause(string? limitParameter, string? offsetParameter) => Inner.LimitOffsetClause(limitParameter, offsetParameter);
    public virtual bool PagingRequiresOrderBy => Inner.PagingRequiresOrderBy;
    public virtual bool SupportsRowValueIn => Inner.SupportsRowValueIn;
    public virtual bool ReportsAffectedRowsBySelect => Inner.ReportsAffectedRowsBySelect;
    public virtual bool SupportsStatementBatching => Inner.SupportsStatementBatching;
    public virtual int? MaxParametersPerMessage => Inner.MaxParametersPerMessage;
    public virtual bool IsTransient(Exception exception) => Inner.IsTransient(exception);
    public virtual string SelectSql(SelectAst select, BindCriteriaParameter bindParameter) => Inner.SelectSql(select, bindParameter);
    public virtual bool SupportsArrayParameters => Inner.SupportsArrayParameters;
    public virtual bool BindsTemporalsNatively => Inner.BindsTemporalsNatively;
    public virtual string ColumnsInfoSql => Inner.ColumnsInfoSql;
    public virtual bool IsDeclaredTypeCompatible(string declaredType, Type clrType, bool enumAsInt) => Inner.IsDeclaredTypeCompatible(declaredType, clrType, enumAsInt);
    public virtual string ViewDefinitionSql => Inner.ViewDefinitionSql;
    public virtual IReadOnlyList<string> CreateTriggerSql(string relation, bool onView, TriggerDefinition trigger) => Inner.CreateTriggerSql(relation, onView, trigger);
    public virtual IReadOnlyList<string> DropTriggerSql(string relation, string triggerName) => Inner.DropTriggerSql(relation, triggerName);
    public virtual string TriggerDefinitionSql => Inner.TriggerDefinitionSql;
    public virtual string IndexesInfoSql => Inner.IndexesInfoSql;
    public virtual string StorageType(EntityMap map, PropertyMap property) => Inner.StorageType(map, property);
    public virtual string Name => Inner.Name;
    public virtual string CreateViewSql(string view, string body, bool materialized) => Inner.CreateViewSql(view, body, materialized);
    public virtual string DropViewSql(string view, bool materialized) => Inner.DropViewSql(view, materialized);
    public virtual string CreateTableSql(TableSchema schema) => Inner.CreateTableSql(schema);
    public virtual string CreateIndexSql(string table, TableSchema.Index index) => Inner.CreateIndexSql(table, index);
    public virtual bool ViewDefinitionIsVerbatim => Inner.ViewDefinitionIsVerbatim;
    public virtual bool SupportsMaterializedViews => Inner.SupportsMaterializedViews;
    public virtual bool SupportsProcedures => Inner.SupportsProcedures;
    public virtual bool SupportsTransactionalDdl => Inner.SupportsTransactionalDdl;
    public virtual DbTransaction BeginMigrationRunLock(DbConnection connection) => Inner.BeginMigrationRunLock(connection);
    public virtual string VersionTableSql => Inner.VersionTableSql;
    public virtual string RenameTableSql(string fromName, string toName) => Inner.RenameTableSql(fromName, toName);
    public virtual string RenameColumnSql(string table, string fromName, string toName) => Inner.RenameColumnSql(table, fromName, toName);
    public virtual string AddColumnSql(string table, string column, string storageType, bool nullable, string? defaultSql) => Inner.AddColumnSql(table, column, storageType, nullable, defaultSql);
    public virtual string DropColumnSql(string table, string column) => Inner.DropColumnSql(table, column);
    public virtual string DropTableSql(string table) => Inner.DropTableSql(table);
    public virtual string DropIndexSql(string table, string index) => Inner.DropIndexSql(table, index);
}
