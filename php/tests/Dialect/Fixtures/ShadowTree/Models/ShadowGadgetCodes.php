<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\View;

/** The view of the shadow round trip: dialect-neutral SQL, so one step serves every dialect. */
#[View('shadow_gadget_codes', 'select id, code from shadow_gadgets')]
final class ShadowGadgetCodes
{
    #[Key]
    #[Column]
    public int $id;

    #[Column]
    public string $code;
}
