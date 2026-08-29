using System.Collections;
using System.Data.Common;

namespace SimpleOrm;

/// <summary>
/// A column-window view over a joined row (ADR-0022 add.1): exposes columns
/// <c>[offset, offset + count)</c> of the parent reader with the alias prefix
/// stripped from their names, so the **one row-mapping pipeline** (§7.11,
/// <see cref="ResultMapper"/>) materializes each entity segment of a join-mode
/// eager row unchanged. Cell access only — cursor movement stays with the owner.
/// </summary>
internal sealed class SegmentReader(DbDataReader parent, int offset, int count, string aliasPrefix) : DbDataReader
{
    public override int FieldCount => count;

    public override string GetName(int ordinal)
    {
        var name = parent.GetName(offset + ordinal);
        return name.StartsWith(aliasPrefix, StringComparison.Ordinal)
            ? name.Substring(aliasPrefix.Length)
            : name;
    }

    public override int GetOrdinal(string name)
    {
        for (var i = 0; i < count; i++)
        {
            if (string.Equals(GetName(i), name, StringComparison.OrdinalIgnoreCase))
            {
                return i;
            }
        }

        throw new IndexOutOfRangeException(name);
    }

    public override Type GetFieldType(int ordinal) => parent.GetFieldType(offset + ordinal);

    public override string GetDataTypeName(int ordinal) => parent.GetDataTypeName(offset + ordinal);

    public override bool IsDBNull(int ordinal) => parent.IsDBNull(offset + ordinal);

    public override object GetValue(int ordinal) => parent.GetValue(offset + ordinal);

    public override bool GetBoolean(int ordinal) => parent.GetBoolean(offset + ordinal);

    public override byte GetByte(int ordinal) => parent.GetByte(offset + ordinal);

    public override long GetBytes(int ordinal, long dataOffset, byte[]? buffer, int bufferOffset, int length)
        => parent.GetBytes(offset + ordinal, dataOffset, buffer, bufferOffset, length);

    public override char GetChar(int ordinal) => parent.GetChar(offset + ordinal);

    public override long GetChars(int ordinal, long dataOffset, char[]? buffer, int bufferOffset, int length)
        => parent.GetChars(offset + ordinal, dataOffset, buffer, bufferOffset, length);

    public override DateTime GetDateTime(int ordinal) => parent.GetDateTime(offset + ordinal);

    public override decimal GetDecimal(int ordinal) => parent.GetDecimal(offset + ordinal);

    public override double GetDouble(int ordinal) => parent.GetDouble(offset + ordinal);

    public override float GetFloat(int ordinal) => parent.GetFloat(offset + ordinal);

    public override Guid GetGuid(int ordinal) => parent.GetGuid(offset + ordinal);

    public override short GetInt16(int ordinal) => parent.GetInt16(offset + ordinal);

    public override int GetInt32(int ordinal) => parent.GetInt32(offset + ordinal);

    public override long GetInt64(int ordinal) => parent.GetInt64(offset + ordinal);

    public override string GetString(int ordinal) => parent.GetString(offset + ordinal);

    public override int GetValues(object[] values)
    {
        var n = Math.Min(values.Length, count);
        for (var i = 0; i < n; i++)
        {
            values[i] = parent.GetValue(offset + i);
        }

        return n;
    }

    public override object this[int ordinal] => GetValue(ordinal);

    public override object this[string name] => GetValue(GetOrdinal(name));

    // Cursor and metadata surface the segment never owns.
    public override int Depth => 0;

    public override bool HasRows => true;

    public override bool IsClosed => false;

    public override int RecordsAffected => 0;

    public override bool NextResult() => throw new NotSupportedException("segment readers expose cells only");

    public override bool Read() => throw new NotSupportedException("segment readers expose cells only");

    public override IEnumerator GetEnumerator() => throw new NotSupportedException("segment readers expose cells only");
}
