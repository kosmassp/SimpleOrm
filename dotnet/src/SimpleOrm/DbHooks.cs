using System.Data.Common;

namespace SimpleOrm;

/// <summary>
/// The declared hooks (Level 3 M3, spec/hooks.md) — metadata the session acts
/// on, never callbacks: <c>[CreatedAt]</c> fills with the session clock on
/// insert when unset; <c>[UpdatedAt]</c> stamps on every write that changes a
/// row (and joins a column-list or dirty SET); <c>[SoftDelete]</c> turns a delete
/// — both forms, and a flushed <c>Remove</c> — into the column-list update that
/// sets the flag, with the stamp and the version rules of an update. Reads never
/// filter on the flag: it is an explicit criteria the caller writes (§2, no
/// hidden WHERE). The clock is <see cref="DbOptions.Clock"/>, UTC.
/// </summary>
public sealed partial class Db
{
    /// <summary>Insert: an unset audit column takes the clock; a caller's value is kept.</summary>
    private void FillCreatedAt(EntityMap map, object entity)
    {
        if (map.CreatedAtProperty is not { } created)
        {
            return;
        }

        var current = created.GetValue(entity);
        var unset = current is null || Equals(current, default(DateTime)) || Equals(current, default(DateTimeOffset));
        if (unset)
        {
            created.SetValue(entity, ClockValue(created));
        }
    }

    /// <summary>Update: the audit column takes the clock — the library is its writer.</summary>
    private void StampUpdatedAt(EntityMap map, object entity)
    {
        if (map.UpdatedAtProperty is { } updated)
        {
            updated.SetValue(entity, ClockValue(updated));
        }
    }

    /// <summary>The SET of a column-list update gains the stamp column when it is mapped and not already listed.</summary>
    private static IReadOnlyList<PropertyMap> WithUpdatedAt(EntityMap map, IReadOnlyList<PropertyMap> set)
    {
        if (map.UpdatedAtProperty is not { } updated || set.Contains(updated))
        {
            return set;
        }

        return [.. set, updated];
    }

    private object ClockValue(PropertyMap property)
    {
        var now = Options.Clock();
        if (now.Kind != DateTimeKind.Utc)
        {
            throw new SimpleOrmException(
                "VAL-020", $"{property.Property.DeclaringType!.Name}.{property.PropertyName}",
                "the session clock must return UTC (DateTimeKind.Utc)");
        }

        var type = Nullable.GetUnderlyingType(property.ClrType) ?? property.ClrType;
        if (type == typeof(DateTimeOffset))
        {
            return new DateTimeOffset(now);
        }

        return now;
    }

    /// <summary>
    /// A delete on a <c>[SoftDelete]</c> entity: the column-list update that sets
    /// the flag (and the <c>[UpdatedAt]</c> stamp). The entity form carries the
    /// version check like any update; the by-key form has no version to check,
    /// so on a versioned entity it refuses (<c>CRUD-012</c>) rather than bump
    /// blindly — pass the entity. The instance is forgotten afterwards like any
    /// deleted row.
    /// </summary>
    private PendingWrite PrepareSoftDelete(EntityMap map, object keyOrEntity, bool byEntity)
    {
        var flag = map.SoftDeleteProperty!;
        var set = WithUpdatedAt(map, [flag]);
        if (byEntity)
        {
            CheckIdentity(map, keyOrEntity);
            var flagBefore = flag.GetValue(keyOrEntity);
            flag.SetValue(keyOrEntity, true);
            try
            {
                return PrepareUpdateStatement(
                    map, keyOrEntity, "delete", UpdateOnlyText(map, set), BoundForUpdate(map, set),
                    () => Forget(map, map.GetKeyValues(keyOrEntity)),
                    restore: () => flag.SetValue(keyOrEntity, flagBefore));
            }
            catch
            {
                flag.SetValue(keyOrEntity, flagBefore);   // a failed write leaves the instance as it was
                throw;
            }
        }

        if (map.VersionProperty is not null)
        {
            throw new SimpleOrmException(
                "CRUD-012", map.EntityType.Name,
                "a soft delete by key cannot bump the version it does not know; pass the entity (the version-checked form)");
        }

        var keyValues = ValidateKey(map, keyOrEntity);
        var command = NewCommand();
        try
        {
            command.CommandText = UpdateOnlyText(map, set);
            AddParameter(command, flag, true);
            if (map.UpdatedAtProperty is { } updated)
            {
                AddParameter(command, updated, ClockValue(updated));
            }

            for (var i = 0; i < keyValues.Length; i++)
            {
                AddParameter(command, map.KeyProperties[i], keyValues[i]);
            }
        }
        catch
        {
            command.Dispose();
            throw;
        }

        var sql = command.CommandText;
        Action? undoForget = null;
        return new PendingWrite(command, AffectedResult, null, generatesKey: false)
        {
            Name = EntityNames.For(map.EntityType).Delete,
            Key = new EntityKey(map.EntityType, keyValues),
            Complete = result =>
            {
                if (AffectedCount(result, sql) == 0)
                {
                    throw new SimpleOrmException(
                        "CRUD-001", map.EntityType.Name, $"delete affected no rows: no row with key ({FormatKey(keyOrEntity)})");
                }

                undoForget = Forget(map, keyValues);
                return 1;
            },
            Undo = () => undoForget?.Invoke(),
        };
    }

    private void AddParameter(DbCommand command, PropertyMap property, object? value)
    {
        var parameter = command.CreateParameter();
        parameter.ParameterName = property.ParameterName;
        _converter.BindValue(parameter, value, property.DeclaringLabel, property.EnumAsInt, property.ClrType);
        command.Parameters.Add(parameter);
    }
}
