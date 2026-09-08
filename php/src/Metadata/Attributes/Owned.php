<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * Owned value types (ADR-0030). On a class: this type is a value object with no
 * table, key, version, generated column, relationship, or nested owned type of
 * its own — never an entity, so loaders, the CLI, and SchemaGuard skip it. On a
 * property of such a type: the opt-in navigation whose `#[Column]` members are
 * stored as columns of the owner's table as `<prefix><column>`; a nullable
 * navigation makes every member column nullable and an all-NULL row leaves it
 * null. Both are required: the class declares what the type is, the property
 * declares that it is mapped.
 */
#[Attribute(Attribute::TARGET_CLASS | Attribute::TARGET_PROPERTY)]
final readonly class Owned
{
    /**
     * @param string|null $prefix column prefix for the members; null derives it from the navigation's
     *                            name through the naming convention plus `_` (`address` → `address_`);
     *                            an empty string disables prefixing
     */
    public function __construct(
        public ?string $prefix = null,
    ) {
    }
}
