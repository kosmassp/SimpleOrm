<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Metadata\EntityMapBuilder;
use SimpleOrm\Metadata\KeyStrategy;

/** The manual fluent builder (§7.2): maps only declared properties, runs the same validations as the attribute loader. */
final class EntityMapBuilderTest extends TestCase
{
    #[Test]
    public function builder_maps_only_declared_properties_with_explicit_names(): void
    {
        $map = EntityMapBuilder::for(Legacy::class)
            ->table('legacy_items')
            ->column('id', name: 'legacy_id', key: true, generated: true)
            ->column('displayName', name: 'display_name')
            ->build();

        self::assertSame('legacy_items', $map->relationName);
        self::assertSame(KeyStrategy::DatabaseGenerated, $map->keyStrategy);
        self::assertSame(['legacy_id', 'display_name'], array_map(static fn ($p) => $p->columnName, $map->properties));
    }

    #[Test]
    public function table_name_falls_back_to_the_naming_convention_when_never_set(): void
    {
        $map = EntityMapBuilder::for(Legacy::class)
            ->column('id', key: true, generated: true)
            ->build();

        self::assertSame('legacy', $map->relationName);
    }

    #[Test]
    public function builder_failures_carry_codes_too(): void
    {
        $builder = EntityMapBuilder::for(Legacy::class)
            ->column('id', name: 'same')
            ->column('displayName', name: 'same', key: true);

        try {
            $builder->build();
            self::fail('expected a MappingException');
        } catch (MappingException $e) {
            self::assertContains('MAP-018', array_map(static fn ($error) => $error->code, $e->errors));
        }
    }
}

// Deliberately no SimpleOrm attributes: the "can't or won't annotate" case.
final class Legacy
{
    public int $id;

    public ?string $displayName = null;
}
