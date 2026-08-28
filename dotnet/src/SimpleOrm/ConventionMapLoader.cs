namespace SimpleOrm;

/// <summary>
/// The convention loader (§7.6): for a type with no mapping attributes at all, every
/// public settable property maps by convention; a property named <c>Id</c> is the
/// key (database-generated for int/long, client GUID for Guid). A conventional
/// entity is always a table, named by the convention.
/// </summary>
internal static class ConventionMapLoader
{
    public static EntityMap Load(Type entityType, INamingConvention convention)
    {
        var errors = new List<MappingError>();
        var specs = new List<MappedPropertySpec>();

        foreach (var property in AttributeMapLoader.PropertiesInDeclarationOrder(entityType))
        {
            if (property.GetMethod is not { IsPublic: true } || property.SetMethod is not { IsPublic: true })
            {
                continue;
            }

            var isId = property.Name == "Id";
            specs.Add(new MappedPropertySpec(property)
            {
                IsKey = isId,
                IsGenerated = isId && (property.PropertyType == typeof(int) || property.PropertyType == typeof(long)),
            });
        }

        var map = MapAssembler.Assemble(
            entityType,
            RelationKind.Table,
            convention.TableName(entityType.Name),
            schema: null,
            statementSql: null,
            statementParameters: [],
            specs,
            indexSpecs: [],
            relationshipSpecs: [],
            convention,
            errors);

        if (map is null)
        {
            throw new MappingException(entityType, errors);
        }

        return map;
    }
}
