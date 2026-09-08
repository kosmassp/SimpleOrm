<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Owned;

/**
 * The owned-type fixture (ADR-0030): a value object with no table of its own,
 * stored as columns of {@see UserProfile} under the `address_` prefix
 * (`address_street`, `address_city`, `address_postal_code`). Only `#[Column]`
 * members; the class-level `#[Owned]` is what keeps it out of the entity set.
 */
#[Owned]
final class Address
{
    #[Column]
    public string $street = '';

    #[Column]
    public string $city = '';

    #[Column]
    public ?string $postalCode = null;
}
