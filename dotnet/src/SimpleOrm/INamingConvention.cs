namespace SimpleOrm;

/// <summary>
/// Translates CLR names to database names wherever a name is derived rather than
/// given explicitly: bare <c>[Column]</c>, a class without <c>[Table]</c>, and
/// derived index names. Configurable per options; the shipped default is
/// <see cref="SnakeCaseNamingConvention"/>. An explicit name in an attribute always
/// bypasses the convention.
/// </summary>
public interface INamingConvention
{
    /// <summary>Column name for a property name (e.g. <c>UserId</c> → <c>user_id</c>).</summary>
    string ColumnName(string propertyName);

    /// <summary>Table name for a CLR type name (e.g. <c>TransactionDetail</c> → <c>transaction_detail</c>; no pluralization).</summary>
    string TableName(string typeName);

    /// <summary>Derived index name for a table and its column names, in index order.</summary>
    string IndexName(string tableName, IReadOnlyList<string> columnNames);
}
