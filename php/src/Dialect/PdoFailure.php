<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDOException;
use Throwable;

/**
 * What a PDO failure says about itself, read the same way by every dialect's `isTransient()` (ADR-0047, spec/dialects.md
 * "Transient open failures"). PHP has no socket or timeout exception types — a `PDOException` is all a driver raises, and
 * the drivers fill it in three shapes (CODING-STANDARD §10):
 *
 * - `pdo_pgsql` and `pdo_sqlsrv`: `errorInfo` is `[SQLSTATE, native code, message]` and `getCode()` is the SQLSTATE;
 * - `pdo_odbc` failing in the constructor: `errorInfo` is null, `getCode()` is the native code, and the message is
 *   `SQLSTATE[08001] SQLDriverConnect: 10061 [Microsoft]…` — the format of the extension's own source, never localized;
 * - a driver that appends further diagnostic records to `errorInfo` (flattened triplets or nested triplets): each one is
 *   read too, so "any of the error records the failure carries" holds where the driver hands them over.
 */
final class PdoFailure
{
    private function __construct()
    {
    }

    /**
     * The failure and every `getPrevious()` link below it, outermost first.
     *
     * @return list<Throwable>
     */
    public static function chain(Throwable $failure): array
    {
        $links = [];
        for ($link = $failure; $link !== null; $link = $link->getPrevious()) {
            $links[] = $link;
        }

        return $links;
    }

    /**
     * Every `[SQLSTATE, native code]` record the link carries (none for a link that is not a `PDOException`); a part the
     * driver did not report is null.
     *
     * @return list<array{0: ?string, 1: ?int}>
     */
    public static function records(Throwable $link): array
    {
        if (!$link instanceof PDOException) {
            return [];
        }

        $records = [];
        $info = $link->errorInfo;
        if (is_array($info) && $info !== []) {
            $records[] = [self::state($info[0] ?? null), self::native($info[1] ?? null)];
            $values = array_values($info);
            for ($i = 3; $i < count($values); $i++) {
                if (is_array($values[$i])) {
                    $nested = array_values($values[$i]);
                    $records[] = [self::state($nested[0] ?? null), self::native($nested[1] ?? null)];
                } elseif ($i % 3 === 0) {
                    $records[] = [self::state($values[$i]), self::native($values[$i + 1] ?? null)];
                }
            }

            return $records;
        }

        // pdo_odbc's constructor form: no errorInfo; the state and the native code are in the message's fixed prefix.
        $message = $link->getMessage();
        $state = preg_match('/^SQLSTATE\[([0-9A-Z]{5})\]/', $message, $match) === 1 ? $match[1] : self::state($link->getCode());
        $native = preg_match('/^SQLSTATE\[[0-9A-Z]{5}\] \w+: (-?\d+) /', $message, $match) === 1
            ? (int) $match[1]
            : (is_int($link->getCode()) ? $link->getCode() : null);

        return [[$state, $native]];
    }

    private static function state(mixed $value): ?string
    {
        return is_string($value) && preg_match('/^[0-9A-Z]{5}$/', $value) === 1 ? $value : null;
    }

    private static function native(mixed $value): ?int
    {
        return match (true) {
            is_int($value) => $value,
            is_string($value) && preg_match('/^-?\d+$/', $value) === 1 => (int) $value,
            default => null,
        };
    }
}
