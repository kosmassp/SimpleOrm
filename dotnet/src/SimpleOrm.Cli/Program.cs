// CLI commands land in milestone 5 (migrate / migrate down / status / validate /
// baseline) and milestone 2 (export-metadata). Until then this is a stub that
// documents the surface without pretending to implement it.

var commands = new[] { "migrate", "migrate down --to <version>", "status", "validate", "baseline", "export-metadata" };

Console.WriteLine("simpleorm — SQL-first micro-ORM CLI (skeleton; commands not implemented yet)");
Console.WriteLine();
Console.WriteLine("Planned commands:");
foreach (var command in commands)
{
    Console.WriteLine($"  simpleorm {command}");
}

return args.Length == 0 ? 0 : 2;
