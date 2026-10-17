<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\SqlServer;

/** A property to hang storage-type probes on (a `PropertyMap` needs a real `ReflectionProperty`). */
final class PropertyStub
{
    public string $value = '';
}
