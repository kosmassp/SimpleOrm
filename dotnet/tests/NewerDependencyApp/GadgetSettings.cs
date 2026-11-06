using Microsoft.Extensions.Options;

namespace NewerDependencyApp;

public sealed class GadgetSettings
{
    public string Prefix { get; set; } = string.Empty;
}

/// <summary>
/// Implements an interface from Microsoft.Extensions.Options 10: loading this type needs that
/// assembly at version 10.0.0.0, so a scan of the assembly's types fails with a
/// <see cref="System.IO.FileLoadException"/> wherever only 9.x can be bound.
/// </summary>
public sealed class GadgetSettingsSetup : IConfigureOptions<GadgetSettings>
{
    public void Configure(GadgetSettings options) => options.Prefix = "g-";
}
