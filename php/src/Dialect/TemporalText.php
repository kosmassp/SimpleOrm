<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

/**
 * The text form a temporal parameter takes under PDO's untyped binding (ADR-0040 add.2; a PHP
 * divergence, CODING-STANDARD §10): PDO sends every temporal as text, and who converts that text
 * into the column's type differs per driver. The server does it for `pdo_sqlite` (stored as-is),
 * `pdo_pgsql`, and `pdo_sqlsrv` — all accept ISO-8601 with the `T` separator and `Z`. ODBC
 * Driver 17 (`pdo_odbc`) converts on the client with the SQL Server ODBC parser, which accepts
 * only `yyyy-mm-dd hh:mm:ss[.fffffff] [+|-]hh:mm` — a `T` or a `Z` is SQLSTATE 22018 ("Invalid
 * character value for cast specification"). The dialect that knows its driver declares the form
 * through {@see DeclaresTemporalText}; the default is ISO-8601.
 */
enum TemporalText
{
    /** ISO-8601 with `T` and `Z` (`2026-08-29T00:00:00.0000000Z`) — the stored form on SQLite, accepted server-side elsewhere. */
    case Iso8601;

    /** The SQL Server ODBC form: a space separator and an explicit `+hh:mm` offset (`2026-08-29 00:00:00.0000000 +00:00`). */
    case Odbc;
}
