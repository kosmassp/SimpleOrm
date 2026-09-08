<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\App;

use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Cli\Fixtures\App\Models\Widget;

/** The fixture application's one registry class (CODING-STANDARD §10): `Application::validate()`/`export-metadata` exercise it. */
final class Registry
{
    public static function allWidgets(): Query
    {
        return Query::inline(EmptyArgs::class, Widget::class, 'select id, name from app_widgets');
    }
}
