<?php

declare(strict_types=1);

namespace SimpleOrm\Mapping;

/**
 * Per-session registry of {@see TypeHandler} instances (§7.9): the extension
 * point beyond the fixed conversion table. Handlers win over the fixed table in
 * both directions — {@see TypeConverter} consults `tryFormat`/`tryParse` first.
 * Mirrors dotnet's `TypeHandlerRegistry` (`Register`, `TryFormat`, `TryParse`);
 * PHP has no `out` parameters, so the by-reference form is the direct mirror.
 */
final class TypeHandlerRegistry
{
    /** @var array<class-string, TypeHandler> */
    private array $handlers = [];

    public function register(TypeHandler $handler): void
    {
        $this->handlers[$handler->type()] = $handler;
    }

    /** @param class-string $class */
    public function has(string $class): bool
    {
        return isset($this->handlers[$class]);
    }

    /**
     * Formats `$value` through its registered handler, if any. Mirrors C#
     * `TryFormat`: only objects can match (handlers are keyed by class-string),
     * so a scalar always misses.
     */
    public function tryFormat(mixed $value, mixed &$formatted): bool
    {
        if (!is_object($value) || !isset($this->handlers[$value::class])) {
            $formatted = null;

            return false;
        }

        $formatted = $this->handlers[$value::class]->format($value);

        return true;
    }

    /**
     * Parses `$databaseValue` through the handler registered for `$class`, if any.
     *
     * @param class-string $class
     */
    public function tryParse(string $class, mixed $databaseValue, mixed &$parsed): bool
    {
        if (!isset($this->handlers[$class])) {
            $parsed = null;

            return false;
        }

        $parsed = $this->handlers[$class]->parse($databaseValue);

        return true;
    }
}
