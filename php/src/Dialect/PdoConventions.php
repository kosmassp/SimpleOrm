<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

/**
 * An optional dialect capability beside the mirrored seam (as Go's `TemporalMarkers`), for what a
 * PDO driver does with untyped parameters and untyped column reads (ADR-0040 add.2; a PHP
 * divergence, CODING-STANDARD §10): the text form temporal parameters take ({@see TemporalText})
 * and the form a binary column comes back in ({@see BinaryText}). A dialect that does not declare
 * it binds ISO-8601 and reads binary raw — `pdo_sqlite` and `pdo_pgsql`.
 */
interface PdoConventions
{
    public function temporalText(): TemporalText;

    public function binaryText(): BinaryText;
}
