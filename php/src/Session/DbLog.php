<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use Closure;

/**
 * The built-in statement-log sink (Level 4 item 4, ADR-0045): each line goes to standard output, so a trial switches the log
 * on with one assignment — `new DbOptions($dialect, log: DbLog::stdout())`. Forwarding to the application's own logger is the
 * application's one-line adapter (`log: fn (string $line) => $logger->debug($line)`), never a library concern; the library
 * brings no logging dependency. The PHP shape of C#'s `DbLog.Console` (CODING-STANDARD §10).
 */
final class DbLog
{
    private function __construct()
    {
    }

    /**
     * A sink writing each line and a newline to the process's standard output (`php://stdout`), not through PHP's output
     * buffers: in the CLI that is the console, and under a web SAPI it still never lands in the response body.
     *
     * @return Closure(string): void
     */
    public static function stdout(): Closure
    {
        $stream = fopen('php://stdout', 'wb');

        return static function (string $line) use ($stream): void {
            if ($stream !== false) {
                fwrite($stream, $line . PHP_EOL);
            }
        };
    }
}
