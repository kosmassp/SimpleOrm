using System.Data.Common;
using Microsoft.Data.Sqlite;

namespace SimpleOrm.Sqlite;

/// <summary>SQLite implementation of <see cref="IDialect"/> backed by Microsoft.Data.Sqlite.</summary>
public sealed class SqliteDialect : IDialect
{
    public DbConnection CreateConnection(string connectionString)
        => new SqliteConnection(connectionString);
}
