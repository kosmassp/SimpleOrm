<?php

declare(strict_types=1);

namespace SimpleOrm\Cli;

/**
 * Command-line option parsing for `bin/simpleorm` (§7.24), mirroring the
 * inline parser in `dotnet/src/SimpleOrm.Cli/Program.cs`: positional tokens
 * (`migrate`, `down`, …), `--name value` options (last one wins, matching the
 * C# reference's `Option()`), and boolean flags that take no value.
 */
final class Arguments
{
    /** @var list<string> */
    private const array BOOLEAN_FLAGS = ['force', 'allow-delete', 'allow-remove', 'amend'];

    /** @var list<string> */
    public readonly array $positional;

    /** @var array<string, list<string>> */
    private readonly array $options;

    /** @param list<string> $argv arguments after the command name (e.g. $argv without argv[0]) */
    public function __construct(array $argv)
    {
        $positional = [];
        $options = [];
        $count = count($argv);
        for ($i = 0; $i < $count; $i++) {
            $token = $argv[$i];
            if (str_starts_with($token, '--')) {
                $name = strtolower(substr($token, 2));
                $options[$name] ??= [];
                if (!in_array($name, self::BOOLEAN_FLAGS, true) && $i + 1 < $count) {
                    $options[$name][] = $argv[++$i];
                }
            } else {
                $positional[] = $token;
            }
        }

        $this->positional = $positional;
        $this->options = $options;
    }

    /** The last `--name value` given, or null. */
    public function option(string $name): ?string
    {
        $values = $this->options[strtolower($name)] ?? [];

        return $values === [] ? null : $values[array_key_last($values)];
    }

    /** Every `--name value` given, in order (repeatable options such as `--rename`). @return list<string> */
    public function all(string $name): array
    {
        return $this->options[strtolower($name)] ?? [];
    }

    public function flag(string $name): bool
    {
        return array_key_exists(strtolower($name), $this->options);
    }
}
