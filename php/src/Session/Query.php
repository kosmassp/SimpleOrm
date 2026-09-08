<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

/**
 * A registered query: SQL bound to its args and result types (§6, §7.5).
 * Mirrors dotnet's `Query<TArgs, TResult>`; PHP has no generics, so both types
 * are carried as `class-string`s (or a scalar type name for a scalar result,
 * e.g. `'int'`) and resolved at execution time by {@see \SimpleOrm\Mapping\ResultMapper}
 * (CODING-STANDARD §10). A registry declares one of these per query as a public
 * static factory method returning `self` — SchemaGuard enumerates those methods.
 */
final class Query
{
    private function __construct(
        public readonly string $argsType,
        public readonly string $resultType,
        public readonly SqlSource $source,
    ) {
    }

    public static function inline(string $argsType, string $resultType, string $sql): self
    {
        return new self($argsType, $resultType, SqlSource::inline($sql));
    }

    public static function fromFile(string $argsType, string $resultType, string $path): self
    {
        return new self($argsType, $resultType, SqlSource::fromFile($path));
    }
}
