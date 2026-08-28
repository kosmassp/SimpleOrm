using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// Reads nullable-reference-type metadata the compiler emits
/// (<c>NullableAttribute</c> on the member, <c>NullableContextAttribute</c> on
/// enclosing scopes), so validation behaves identically on net10.0 and
/// netstandard2.0 — <c>NullabilityInfoContext</c> is net6+ only (CLAUDE.md §4).
/// </summary>
internal static class NullabilityReader
{
    private const byte Nullable = 2;

    /// <summary>True when the property's type admits null: <c>Nullable&lt;T&gt;</c> or a nullable-annotated reference type.</summary>
    public static bool IsNullable(PropertyInfo property)
    {
        var type = property.PropertyType;
        if (type.IsValueType)
        {
            return System.Nullable.GetUnderlyingType(type) is not null;
        }

        // Reference type: the first byte of NullableAttribute describes the top-level type.
        foreach (var attribute in property.CustomAttributes)
        {
            if (attribute.AttributeType.FullName == "System.Runtime.CompilerServices.NullableAttribute")
            {
                var argument = attribute.ConstructorArguments[0];
                if (argument.ArgumentType == typeof(byte))
                {
                    return (byte)argument.Value! == Nullable;
                }

                var flags = (IReadOnlyList<CustomAttributeTypedArgument>)argument.Value!;
                return flags.Count > 0 && (byte)flags[0].Value! == Nullable;
            }
        }

        // No member attribute: the enclosing NullableContext decides.
        for (var scope = property.DeclaringType; scope is not null; scope = scope.DeclaringType)
        {
            foreach (var attribute in scope.CustomAttributes)
            {
                if (attribute.AttributeType.FullName == "System.Runtime.CompilerServices.NullableContextAttribute")
                {
                    return (byte)attribute.ConstructorArguments[0].Value! == Nullable;
                }
            }
        }

        foreach (var attribute in property.DeclaringType!.Module.CustomAttributes)
        {
            if (attribute.AttributeType.FullName == "System.Runtime.CompilerServices.NullableContextAttribute")
            {
                return (byte)attribute.ConstructorArguments[0].Value! == Nullable;
            }
        }

        // No NRT metadata at all (oblivious code): treat reference types as nullable.
        return true;
    }
}
