<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Support;

use DateTimeImmutable;

/**
 * A plain property bag mirroring the fixture `User`'s columns
 * (`php/tests/Sample/Models/User.php` + `BaseModel.php`), for a hand-built
 * `EntityMap` in `SchemaSnapshotTest` — pinned by `conformance/snapshot-cases/users_v7.json`.
 * Carries no mapping attributes; see `Widget` for why.
 */
final class UserShape
{
    public int $id;

    public string $name = '';

    public string $email = '';

    public ?string $displayName = null;

    public DateTimeImmutable $createdAtUtc;

    public ?DateTimeImmutable $updatedAtUtc = null;
}
