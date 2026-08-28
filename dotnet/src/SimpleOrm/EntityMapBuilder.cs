using System.Linq.Expressions;
using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// The manual loader (§7.2): a fluent map for types you can't or won't annotate.
/// Registered via <see cref="MappingOptions.Register{T}"/>, where it takes precedence
/// over attributes and conventions. Mapping stays opt-in: only properties named with
/// <see cref="Property{TProp}"/> are mapped.
/// </summary>
public sealed class EntityMapBuilder<T>
    where T : class
{
    private readonly List<MappedPropertySpec> _specs = [];
    private string? _tableName;
    private string? _schema;

    /// <summary>Sets the table name; when never called, the naming convention derives it from the type name.</summary>
    public EntityMapBuilder<T> ToTable(string name, string? schema = null)
    {
        _tableName = name;
        _schema = schema;
        return this;
    }

    /// <summary>Maps a property; chain the returned configuration for column name, key, generated, version, or enum storage.</summary>
    public PropertyConfiguration Property<TProp>(Expression<Func<T, TProp>> selector)
    {
        var body = selector.Body is UnaryExpression { NodeType: ExpressionType.Convert } unary
            ? unary.Operand
            : selector.Body;
        if (body is not MemberExpression { Member: PropertyInfo property })
        {
            throw new ArgumentException("The selector must be a simple property access, e.g. x => x.Name.", nameof(selector));
        }

        var spec = new MappedPropertySpec(property);
        _specs.Add(spec);
        return new PropertyConfiguration(spec);
    }

    internal EntityMap Build(INamingConvention convention)
    {
        var errors = new List<MappingError>();
        var map = MapAssembler.Assemble(
            typeof(T),
            RelationKind.Table,
            _tableName ?? convention.TableName(typeof(T).Name),
            _schema,
            statementSql: null,
            statementParameters: [],
            _specs,
            indexSpecs: [],
            relationshipSpecs: [],
            convention,
            errors);

        if (map is null)
        {
            throw new MappingException(typeof(T), errors);
        }

        return map;
    }

    /// <summary>Fluent configuration of one mapped property.</summary>
    public sealed class PropertyConfiguration
    {
        private readonly MappedPropertySpec _spec;

        internal PropertyConfiguration(MappedPropertySpec spec) => _spec = spec;

        /// <summary>Binds an explicit column name instead of the convention-derived one.</summary>
        public PropertyConfiguration Column(string name)
        {
            _spec.ExplicitColumn = name;
            return this;
        }

        public PropertyConfiguration Key()
        {
            _spec.IsKey = true;
            return this;
        }

        public PropertyConfiguration Generated()
        {
            _spec.IsGenerated = true;
            return this;
        }

        public PropertyConfiguration Version()
        {
            _spec.IsVersion = true;
            return this;
        }

        public PropertyConfiguration EnumAsInt()
        {
            _spec.EnumAsInt = true;
            return this;
        }
    }
}
