<?php

declare(strict_types=1);

namespace SimpleOrm\Naming;

/**
 * The default convention (spec/metadata-model.md "Naming convention"): PascalCase
 * or camelCase to snake_case. The algorithm is part of the spec — every port must
 * produce identical database names from its own idiom — pinned by the normative
 * vectors there and mirrored from the C# reference's SnakeCaseNamingConvention.
 */
final class SnakeCaseNamingConvention implements NamingConvention
{
    public function toDatabase(string $name): string
    {
        return self::toSnakeCase($name);
    }

    /**
     * An underscore is inserted before an upper-case letter that follows a
     * lower-case letter or digit, or that starts the last word of an acronym run
     * (an upper followed by a lower); everything is lower-cased.
     */
    private static function toSnakeCase(string $name): string
    {
        $length = strlen($name);
        if ($length === 0) {
            return $name;
        }

        $result = '';
        for ($i = 0; $i < $length; $i++) {
            $c = $name[$i];
            if (self::isUpper($c) && $i > 0) {
                $previous = $name[$i - 1];
                $startsWordAfterLowerOrDigit = self::isLower($previous) || self::isDigit($previous);
                $endsAcronymRun = self::isUpper($previous) && $i + 1 < $length && self::isLower($name[$i + 1]);
                if ($startsWordAfterLowerOrDigit || $endsAcronymRun) {
                    $result .= '_';
                }
            }

            $result .= strtolower($c);
        }

        return $result;
    }

    private static function isUpper(string $c): bool
    {
        return $c >= 'A' && $c <= 'Z';
    }

    private static function isLower(string $c): bool
    {
        return $c >= 'a' && $c <= 'z';
    }

    private static function isDigit(string $c): bool
    {
        return $c >= '0' && $c <= '9';
    }
}
