<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use LogicException;
use PDO;
use PDOStatement;

use function is_numeric;

/**
 * Reads the results a statement returns that the session cares about, in order (spec/dialects.md "Reading results"):
 * the generated key of an insert, a dialect's reported affected-row count. Each is **the first column of the first row
 * of the next result that has columns** — a count-only result a driver surfaces first (PDO does, for SQL Server's
 * `insert … ; select scope_identity()` and for any statement run with counts on) is skipped. One reader per
 * executed statement; a multi-statement message (SQL Server) is read by calling {@see self::scalar()} once per
 * statement that returns a result, which steps past the previous result with `nextRowset()` first.
 *
 * @internal
 */
final class ResultReader
{
    private bool $started = false;

    public function __construct(private readonly PDOStatement $statement)
    {
    }

    /**
     * What an executed single statement returns that the session reads (ADR-0044): its generated key, its judged
     * count — the driver's, or the dialect's reported scalar — a registry command's driver count, or nothing.
     * The one reading the immediate writes and {@see WriteRun}'s one-at-a-time realization share (CODING-STANDARD §8).
     */
    public static function read(PDOStatement $statement, ResultKind $kind, bool $countsBySelect): mixed
    {
        return match ($kind) {
            ResultKind::Key => self::first($statement),
            ResultKind::Count => $countsBySelect ? self::reportedCount(self::first($statement)) : $statement->rowCount(),
            ResultKind::CommandCount => $statement->rowCount(),
            ResultKind::None => null,
        };
    }

    /**
     * A dialect's reported count as an integer (spec/dialects.md Clarification 24): `pdo_odbc` hands it back as a
     * string, hence the cast; a batch that yields none is a dialect bug, never zero — so never `CRUD-010`/`CRUD-001`.
     */
    public static function reportedCount(mixed $count): int
    {
        if ($count === null || !is_numeric($count)) {
            throw new LogicException('the generated write did not report its affected-row count; the dialect renders it with a trailing select');
        }

        return (int) $count;
    }

    private static function first(PDOStatement $statement): mixed
    {
        $reader = new self($statement);
        $value = $reader->scalar();
        $reader->finish();

        return $value;
    }

    /** The next result's first column of its first row, or null when no further result has columns. */
    public function scalar(): mixed
    {
        if ($this->started) {
            $this->statement->nextRowset();   // step past the result read before
        }

        $this->started = true;
        while ($this->statement->columnCount() === 0 && $this->statement->nextRowset()) {
            // advance to the result that carries the value
        }

        if ($this->statement->columnCount() === 0) {
            return null;
        }

        $row = $this->statement->fetch(PDO::FETCH_NUM);

        return is_array($row) ? ($row[0] ?? null) : null;
    }

    /**
     * Steps through every result nobody read. In a multi-statement message the server runs the statements one after the
     * other and reports each failure in that statement's own result slot, so a failure in a statement whose result the
     * session does not read (an insert with a caller-supplied key, a trailing `set nocount off`) surfaces only when the
     * driver is stepped past it — and `closeCursor()` would discard it, leaving the call to commit around a statement that
     * failed.
     */
    public function drain(): void
    {
        while ($this->statement->nextRowset()) {
            // reading nothing: the stepping itself raises a pending error
        }
    }

    /** Releases the statement's remaining results. Not between the reads of one message: it discards every result left. */
    public function finish(): void
    {
        $this->statement->closeCursor();
    }
}
