<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Errors\SimpleOrmException;

/**
 * The opt-in `REL-004` guard for entity classes (ADR-0032 ruling 2,
 * spec/loading.md "The guard across languages"). The mapper `unset()`s a
 * database-read entity's collection navigations, so the typed property is
 * uninitialized and PHP routes a read through `__get` — which this trait
 * turns into `SimpleOrmException('REL-004', …)`: unloaded is not empty, and a
 * navigation nobody loaded is a bug, not an empty result. Without the trait
 * the read is PHP's own `Error` ("must not be accessed before
 * initialization") — still a refusal, never a silent `[]`. Loading (explicit,
 * batch, or eager) re-initializes the property, after which `__get` is never
 * consulted again. Entities constructed by user code keep their initializers.
 * A CODING-STANDARD §10 row: the library cannot add `__get` to a user class.
 */
trait Navigations
{
    public function __get(string $name): mixed
    {
        if (property_exists($this, $name)) {
            throw new SimpleOrmException(
                'REL-004',
                static::class . '.' . $name,
                'the navigation was not loaded; load it explicitly ($db->load($entity, ' . var_export($name, true)
                    . ') or loadEach()) or eagerly (include(' . var_export($name, true) . ')) — nothing loads on access',
            );
        }

        trigger_error(sprintf('Undefined property: %s::$%s', static::class, $name), E_USER_WARNING);

        return null;
    }
}
