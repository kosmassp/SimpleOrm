<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Metadata\MappingOptions;

/** Options fixed at {@see Db::open}: the dialect, metadata configuration, and the type-handler registry (§7.17). */
final readonly class DbOptions
{
    public function __construct(
        public Dialect $dialect,
        public MappingOptions $mapping = new MappingOptions(),
        public TypeHandlerRegistry $typeHandlers = new TypeHandlerRegistry(),
    ) {
    }
}
