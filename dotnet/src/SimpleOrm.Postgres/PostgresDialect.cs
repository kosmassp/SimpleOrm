using System.Data.Common;
using Npgsql;

namespace SimpleOrm.Postgres;

/// <summary>PostgreSQL implementation of <see cref="IDialect"/> backed by Npgsql.</summary>
public sealed class PostgresDialect : IDialect
{
    public DbConnection CreateConnection(string connectionString)
        => new NpgsqlConnection(connectionString);
}
