<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * Navigation cardinality (ADR-0005/0019). Polymorphic and "through" relations are
 * ruled out permanently (ADR-0019 add.1). The backing value is the export token.
 */
enum RelationshipKind: string
{
    case ManyToOne = 'many_to_one';
    case OneToMany = 'one_to_many';
    case ManyToMany = 'many_to_many';
    case OneToOne = 'one_to_one';
}
