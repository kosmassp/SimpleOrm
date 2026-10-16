<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;
use SimpleOrm\Metadata\CascadeOperation;

/**
 * Declares what a navigation cascades in the unit of work (Level 3 M2, unit-of-work.md "Cascades"):
 * `#[Cascade(CascadeOperation::Persist, CascadeOperation::Remove)]`. Valid on a `#[ManyToOne]`,
 * `#[OneToOne]`, or `#[OneToMany]` navigation only — on anything else, or on a `#[ManyToMany]`
 * navigation (its link rows are entities the unit of work persists explicitly), it is `MAP-025`.
 * The operations normalize to declaration order without repeats, so the export is stable.
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class Cascade
{
    /** @var list<CascadeOperation> */
    public array $operations;

    public function __construct(CascadeOperation ...$operations)
    {
        $this->operations = array_values(array_filter(
            CascadeOperation::cases(),
            static fn (CascadeOperation $case): bool => in_array($case, $operations, true),
        ));
    }
}
