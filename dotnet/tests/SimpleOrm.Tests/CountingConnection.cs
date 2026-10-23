using System.Data;
using System.Data.Common;
using System.Diagnostics.CodeAnalysis;

namespace SimpleOrm.Tests;

/// <summary>
/// A connection that records which command objects executed — to see that the
/// sequential batching realization reuses one prepared command per statement text
/// (ADR-0044). Everything else forwards to the real connection.
/// </summary>
internal sealed class CountingConnection(DbConnection inner, HashSet<DbCommand> executed) : DbConnection
{
    [AllowNull]
    public override string ConnectionString
    {
        get => inner.ConnectionString;
        set => inner.ConnectionString = value;
    }

    public override string Database => inner.Database;

    public override string DataSource => inner.DataSource;

    public override string ServerVersion => inner.ServerVersion;

    public override ConnectionState State => inner.State;

    public override void ChangeDatabase(string databaseName) => inner.ChangeDatabase(databaseName);

    public override void Close() => inner.Close();

    public override void Open() => inner.Open();

    public override Task OpenAsync(CancellationToken cancellationToken) => inner.OpenAsync(cancellationToken);

    protected override DbTransaction BeginDbTransaction(IsolationLevel isolationLevel) => inner.BeginTransaction(isolationLevel);

    protected override DbCommand CreateDbCommand() => new CountingCommand(inner.CreateCommand(), this, executed);

    protected override void Dispose(bool disposing)
    {
        if (disposing)
        {
            inner.Dispose();
        }

        base.Dispose(disposing);
    }

    private sealed class CountingCommand(DbCommand inner, DbConnection connection, HashSet<DbCommand> executed) : DbCommand
    {
        [AllowNull]
        public override string CommandText
        {
            get => inner.CommandText;
            set => inner.CommandText = value;
        }

        public override int CommandTimeout
        {
            get => inner.CommandTimeout;
            set => inner.CommandTimeout = value;
        }

        public override CommandType CommandType
        {
            get => inner.CommandType;
            set => inner.CommandType = value;
        }

        public override bool DesignTimeVisible
        {
            get => inner.DesignTimeVisible;
            set => inner.DesignTimeVisible = value;
        }

        public override UpdateRowSource UpdatedRowSource
        {
            get => inner.UpdatedRowSource;
            set => inner.UpdatedRowSource = value;
        }

        protected override DbConnection? DbConnection
        {
            get => connection;
            set { }
        }

        protected override DbParameterCollection DbParameterCollection => inner.Parameters;

        protected override DbTransaction? DbTransaction
        {
            get => inner.Transaction;
            set => inner.Transaction = value;
        }

        public override void Cancel() => inner.Cancel();

        public override void Prepare() => inner.Prepare();

        protected override DbParameter CreateDbParameter() => inner.CreateParameter();

        public override int ExecuteNonQuery()
        {
            executed.Add(this);
            return inner.ExecuteNonQuery();
        }

        public override object? ExecuteScalar()
        {
            executed.Add(this);
            return inner.ExecuteScalar();
        }

        protected override DbDataReader ExecuteDbDataReader(CommandBehavior behavior)
        {
            executed.Add(this);
            return inner.ExecuteReader(behavior);
        }

        protected override void Dispose(bool disposing)
        {
            if (disposing)
            {
                inner.Dispose();
            }

            base.Dispose(disposing);
        }
    }
}
