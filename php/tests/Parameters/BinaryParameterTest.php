<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Parameters;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Parameters\BinaryParameter;
use SimpleOrm\Parameters\ParameterBinder;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Support\AvatarArgs;
use SimpleOrm\Tests\Support\BytesRoundTrip;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * A `bytes`-typed value binds as `PDO::PARAM_LOB`, chosen by the metadata token (ADR-0040
 * add.1): PHP strings carry no text/bytes distinction, and `PARAM_STR` would store TEXT —
 * which a STRICT `BLOB` column refuses on SQLite, and which PostgreSQL parses as escaped text.
 */
final class BinaryParameterTest extends TestCase
{
    private TempDatabase $fixture;

    private ?Db $db = null;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
    }

    protected function tearDown(): void
    {
        $this->db?->close();
        $this->fixture->delete();
    }

    #[Test]
    public function the_converter_wraps_only_a_bytes_token(): void
    {
        $converter = new TypeConverter(new TypeHandlerRegistry());

        $bound = $converter->toParameter("\x00\xff", 'ctx', type: ColumnType::Bytes);
        self::assertInstanceOf(BinaryParameter::class, $bound);
        self::assertSame("\x00\xff", $bound->bytes);

        self::assertSame('text', $converter->toParameter('text', 'ctx', type: ColumnType::String));
        self::assertSame('text', $converter->toParameter('text', 'ctx'));
        self::assertNull($converter->toParameter(null, 'ctx', type: ColumnType::Bytes));
        self::assertSame("\x00\xff", $converter->toDatabase("\x00\xff", 'ctx', type: ColumnType::Bytes));   // snapshots stay plain
    }

    #[Test]
    public function a_registry_argument_declares_its_token_with_column(): void
    {
        $bound = ParameterBinder::bind(
            'select 1 where @avatar is not null',
            new AvatarArgs("\x00"),
            'q',
            new TypeConverter(new TypeHandlerRegistry()),
        );

        self::assertInstanceOf(BinaryParameter::class, $bound->parameters['avatar']);
    }

    #[Test]
    public function bytes_round_trip_through_crud_criteria_and_registry_queries_on_sqlite(): void
    {
        $this->db = SampleDatabase::open($this->fixture);

        BytesRoundTrip::run($this->db);
    }
}
