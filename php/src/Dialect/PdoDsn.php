<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

/**
 * The one place a PDO DSN's `key=value` entries are edited (ADR-0043): the shadow hosts turn the admin connection
 * (`dbname=postgres`, `Database=master`) into the connection that opens the throwaway database, and the live test legs
 * do the same for their per-fixture databases. A DSN is `driver:key=value;key=value` — the entry is replaced when the
 * key is present (case-insensitive, keeping the DSN's own spelling, also as the first entry behind the driver prefix) and appended when it is not.
 */
final class PdoDsn
{
    private function __construct()
    {
    }

    public static function withValue(string $dsn, string $key, string $value): string
    {
        $pattern = '/^(?<lead>\s*(?:[A-Za-z_]+:)?)(?<key>' . preg_quote($key, '/') . ')=/i';
        $parts = [];
        $replaced = false;
        foreach (explode(';', $dsn) as $part) {
            if (preg_match($pattern, $part, $matches) === 1) {
                $parts[] = $matches['lead'] . $matches['key'] . '=' . $value;   // the DSN's own spelling of the key
                $replaced = true;

                continue;
            }

            $parts[] = $part;
        }

        if (!$replaced) {
            $parts[] = $key . '=' . $value;
        }

        return implode(';', $parts);
    }
}
