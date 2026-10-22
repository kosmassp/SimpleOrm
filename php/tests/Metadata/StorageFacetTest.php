<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Owned;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMapBuilder;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Types\Decimal;

/**
 * The storage facets (Level 4, ADR-0043, spec/metadata-model.md "Storage facets"): `#[Column(length: …)]` on a string or
 * bytes column, `#[Column(precision: …, scale: …)]` on a decimal — carried on the property, exported only when declared,
 * and every misplaced one exactly `MAP-032` through the one assembler rule both loaders share.
 */
final class StorageFacetTest extends TestCase
{
    #[Test]
    public function the_fixture_declares_the_transaction_amount_at_precision_18_scale_2(): void
    {
        $map = (new EntityMapLoader())->load(Transaction::class);
        $amount = null;
        foreach ($map->properties as $property) {
            if ($property->columnName === 'amount') {
                $amount = $property;
            }
        }

        self::assertNotNull($amount);
        self::assertSame(18, $amount->precision);
        self::assertSame(2, $amount->scale);
        self::assertNull($amount->length);
    }

    #[Test]
    public function the_export_carries_a_facet_only_when_declared_after_the_other_optional_keys(): void
    {
        $loader = new EntityMapLoader();
        $document = json_decode(EntityMapJson::export($loader->load(FacetOk::class), $loader), true, flags: JSON_THROW_ON_ERROR);

        $byColumn = array_column($document['columns'], null, 'column');
        self::assertSame(['column', 'type', 'nullable', 'length'], array_keys($byColumn['label']));
        self::assertSame(50, $byColumn['label']['length']);
        self::assertSame(['column', 'type', 'nullable', 'precision', 'scale'], array_keys($byColumn['price']));
        self::assertSame([12, 3], [$byColumn['price']['precision'], $byColumn['price']['scale']]);
        self::assertArrayNotHasKey('length', $byColumn['plain']);
        self::assertArrayNotHasKey('precision', $byColumn['plain']);
    }

    #[Test]
    public function a_scale_left_out_beside_a_precision_is_zero_and_still_exported_beside_it(): void
    {
        $loader = new EntityMapLoader();
        $map = $loader->load(FacetOk::class);
        $whole = null;
        foreach ($map->properties as $property) {
            if ($property->columnName === 'whole') {
                $whole = $property;
            }
        }

        self::assertNotNull($whole);
        self::assertSame([10, 0], [$whole->precision, $whole->scale]);
        $document = json_decode(EntityMapJson::export($map, $loader), true, flags: JSON_THROW_ON_ERROR);
        $entry = array_column($document['columns'], null, 'column')['whole'];
        self::assertSame([10, 0], [$entry['precision'], $entry['scale']]);
    }

    #[Test]
    public function a_length_may_sit_on_a_bytes_column(): void
    {
        $map = (new EntityMapLoader())->load(FacetBytes::class);

        self::assertSame(16, $map->properties[1]->length);
    }

    #[Test]
    public function a_facet_may_sit_on_an_owned_types_member(): void
    {
        $map = (new EntityMapLoader())->load(FacetOwnerOk::class);
        $columns = array_column(array_map(
            static fn ($p): array => ['column' => $p->columnName, 'length' => $p->length],
            $map->properties,
        ), 'length', 'column');

        self::assertSame(['id' => null, 'seal_text' => 20], $columns);
    }

    /** @return array<string, array{class-string, string}> */
    public static function misplacedAttributeFacets(): array
    {
        return [
            'length on an integer' => [FacetLengthOnInt::class, 'length applies to a string or bytes column'],
            'length on a decimal' => [FacetLengthOnDecimal::class, 'length applies to a string or bytes column'],
            'precision on a string' => [FacetPrecisionOnString::class, 'precision and scale apply to a decimal column'],
            'a scale without a precision' => [FacetScaleWithoutPrecision::class, 'scale needs a precision'],
            'a scale above its precision' => [FacetScaleAbovePrecision::class, 'scale must be between 0 and the precision'],
            'a zero length' => [FacetZeroLength::class, 'length must be positive'],
            'a zero precision' => [FacetZeroPrecision::class, 'precision must be positive'],
            'a facet on a view' => [FacetOnView::class, 'only valid on a table-backed entity'],
        ];
    }

    /** @param class-string $entityType */
    #[Test]
    #[DataProvider('misplacedAttributeFacets')]
    public function a_misplaced_attribute_facet_is_exactly_map032(string $entityType, string $reason): void
    {
        try {
            (new EntityMapLoader())->load($entityType);
            self::fail("{$entityType} was expected to fail loading");
        } catch (MappingException $e) {
            self::assertNotSame([], $e->errors);
            foreach ($e->errors as $error) {
                self::assertSame('MAP-032', $error->code, (string) $error);
            }

            self::assertStringContainsString($reason, (string) $e->errors[0]);
        }
    }

    /** @return array<string, array{callable(EntityMapBuilder): EntityMapBuilder}> */
    public static function misplacedBuilderFacets(): array
    {
        return [
            'length on an integer' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)->column('count', length: 5)],
            'precision on a string' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)->column('label', precision: 5)],
            'scale without precision' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)->column('price', scale: 2)],
            'scale above precision' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)->column('price', precision: 3, scale: 4)],
            'negative length' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)->column('label', length: -1)],
        ];
    }

    /** @param callable(EntityMapBuilder): EntityMapBuilder $declare */
    #[Test]
    #[DataProvider('misplacedBuilderFacets')]
    public function the_manual_builder_refuses_a_misplaced_facet_as_exactly_map032(callable $declare): void
    {
        try {
            $declare(EntityMapBuilder::for(FacetBuilt::class)->table('facet_built'))->build();
            self::fail('the misplaced facet was expected to fail the build');
        } catch (MappingException $e) {
            self::assertNotSame([], $e->errors);
            foreach ($e->errors as $error) {
                self::assertSame('MAP-032', $error->code, (string) $error);
            }
        }
    }

    #[Test]
    public function the_manual_builder_declares_the_same_facets_the_attributes_do(): void
    {
        $map = EntityMapBuilder::for(FacetBuilt::class)->table('facet_built')
            ->column('id', key: true)
            ->column('label', length: 40)
            ->column('price', precision: 9, scale: 1)
            ->build();

        $facets = [];
        foreach ($map->properties as $property) {
            $facets[$property->columnName] = [$property->length, $property->precision, $property->scale];
        }

        self::assertSame(['id' => [null, null, null], 'label' => [40, null, null], 'price' => [null, 9, 1]], $facets);
    }
}

/** Every valid facet shape. */
#[Table('facet_ok')]
final class FacetOk
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(length: 50)]
    public string $label;

    #[Column(precision: 12, scale: 3)]
    public Decimal $price;

    #[Column(precision: 10)]
    public Decimal $whole;

    #[Column]
    public string $plain = '';
}

/** A facet on a bytes column (length applies to strings and bytes alike). */
#[Table('facet_bytes')]
final class FacetBytes
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(type: ColumnType::Bytes, length: 16)]
    public string $digest;
}

#[Table('facet_owner_ok')]
final class FacetOwnerOk
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Owned]
    public ?FacetSeal $seal = null;
}

#[Owned]
final class FacetSeal
{
    #[Column(length: 20)]
    public string $text = '';
}

#[Table('facet_length_on_int')]
final class FacetLengthOnInt
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(length: 5)]
    public int $count = 0;
}

#[Table('facet_length_on_decimal')]
final class FacetLengthOnDecimal
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(length: 5)]
    public Decimal $price;
}

#[Table('facet_precision_on_string')]
final class FacetPrecisionOnString
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(precision: 5, scale: 1)]
    public string $label = '';
}

#[Table('facet_scale_without_precision')]
final class FacetScaleWithoutPrecision
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(scale: 2)]
    public Decimal $price;
}

#[Table('facet_scale_above_precision')]
final class FacetScaleAbovePrecision
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(precision: 3, scale: 4)]
    public Decimal $price;
}

#[Table('facet_zero_length')]
final class FacetZeroLength
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(length: 0)]
    public string $label = '';
}

#[Table('facet_zero_precision')]
final class FacetZeroPrecision
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column(precision: 0)]
    public Decimal $price;
}

/** A view has no column DDL to size. */
#[View('facet_on_view', 'select 1 as id, \'x\' as label')]
final class FacetOnView
{
    #[Key]
    #[Column]
    public int $id;

    #[Column(length: 10)]
    public string $label = '';
}

/** The builder's target: plain public properties, mapped by name. */
final class FacetBuilt
{
    public int $id;

    public int $count = 0;

    public string $label = '';

    public Decimal $price;
}
