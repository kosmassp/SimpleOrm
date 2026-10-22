<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * One body token of a declared trigger (Level 4, ADR-0042, spec/metadata-model.md "Triggers"): `{new: a, b}` — the
 * inserted or updated rows as a relation, `{old: a, b}` — the deleted or pre-update rows, `{now}` — UTC now in the
 * §7.9 storage form. The only non-SQL in a body; produced by {@see TriggerBody::parse()}.
 */
final readonly class TriggerToken
{
    /** @param list<string> $columns column names of the entity's relation (empty for `{now}`) */
    public function __construct(
        public TriggerTokenKind $kind,
        public array $columns = [],
    ) {
    }

    /** The one canonical spelling — `{new: a, b}`, `{old: a, b}`, `{now}` — the export, the snapshot, and the shadow's fold all share. */
    public function canonical(): string
    {
        return match ($this->kind) {
            TriggerTokenKind::Now => '{now}',
            TriggerTokenKind::New => '{new: ' . implode(', ', $this->columns) . '}',
            TriggerTokenKind::Old => '{old: ' . implode(', ', $this->columns) . '}',
        };
    }
}
