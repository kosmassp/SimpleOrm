<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Support;

/**
 * A plain property bag for hand-built `EntityMap`s in Migrations tests (mirrors
 * `dotnet/tests/SimpleOrm.Tests/GeneratorFixtures.cs`'s `GenModels.Widget`).
 * Carries no mapping attributes — the tests construct `EntityMap`/`PropertyMap`
 * directly, bypassing the not-yet-landed `EntityMapLoader`; only the property
 * names need to exist, for `ReflectionProperty` binding.
 */
final class Widget
{
    public int $id;

    public string $name = '';

    public ?string $note = null;
}
