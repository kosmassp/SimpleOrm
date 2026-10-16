<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Metadata\EntityMap;

/**
 * One identity-map entry (ADR-0034 add.1): the session's instance for a (type, key), the
 * key token it was registered under ({@see \SimpleOrm\Metadata\KeyTuple::token()} — so a
 * later change of the instance's key is detectable), and the snapshot of its mapped values
 * as read or last written. Immutable: re-snapshotting replaces the entry.
 *
 * @internal owned by {@see DbTracking}
 */
final readonly class TrackedInstance
{
    /** @param list<mixed> $snapshot one value per `$map->properties` entry, in that order (owned members flat, ADR-0030) */
    public function __construct(
        public object $instance,
        public EntityMap $map,
        public string $token,
        public array $snapshot,
    ) {
    }

    public function withSnapshot(array $snapshot): self
    {
        return new self($this->instance, $this->map, $this->token, $snapshot);
    }
}
