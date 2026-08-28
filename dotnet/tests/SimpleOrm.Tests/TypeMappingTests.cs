using System.Globalization;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>Milestone 4: the fixed conversion table, strict construction, handlers, and their error codes.</summary>
[Collection(SqliteCollection.Name)]
public sealed class TypeMappingTests(SqliteFixture fixture)
{
    // Every fixed-table type in one entity (§7.9), created and round-tripped via generated code.
    [Table("type_zoo")]
    public sealed class TypeZoo
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public int I32 { get; set; }

        [Column]
        public short I16 { get; set; }

        [Column]
        public decimal Price { get; set; }

        [Column]
        public double D { get; set; }

        [Column]
        public float F { get; set; }

        [Column]
        public bool Flag { get; set; }

        [Column]
        public required string Text { get; set; }

        [Column]
        public string? MaybeText { get; set; }

        [Column]
        public Guid Token { get; set; }

        [Column]
        public byte[] Blob { get; set; } = [];

        [Column]
        public DateTime At { get; set; }

        [Column]
        public DateTimeOffset AtOffset { get; set; }

        [Column]
        public DateOnly Day { get; set; }

        [Column]
        public TimeOnly Clock { get; set; }

        [Column]
        public TransactionStatus AsText { get; set; }

        [Column]
        [EnumAsInt]
        public TransactionStatus AsInt { get; set; }

        [Column]
        public TransactionStatus? MaybeEnum { get; set; }
    }

    [Fact]
    public async Task Fixed_table_round_trips_every_type()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await db.CreateTableAsync<TypeZoo>(CancellationToken.None);

        var zoo = new TypeZoo
        {
            I32 = 42,
            I16 = 7,
            Price = 1234.56m,
            D = 2.25,
            F = 1.5f,
            Flag = true,
            Text = "hello",
            MaybeText = null,
            Token = Guid.Parse("6f9619ff-8b86-d011-b42d-00cf4fc964ff"),
            Blob = [1, 2, 3],
            At = new DateTime(2026, 8, 28, 10, 0, 0, DateTimeKind.Utc),
            AtOffset = new DateTimeOffset(2026, 8, 28, 12, 0, 0, TimeSpan.FromHours(2)),
            Day = new DateOnly(2026, 8, 28),
            Clock = new TimeOnly(13, 30, 15),
            AsText = TransactionStatus.Completed,
            AsInt = TransactionStatus.Cancelled,
            MaybeEnum = null,
        };
        await db.InsertAsync(zoo, CancellationToken.None);

        var loaded = Assert.Single(await db.QueryAllAsync<TypeZoo>(CancellationToken.None));

        Assert.Equal(zoo.I32, loaded.I32);
        Assert.Equal(zoo.I16, loaded.I16);
        Assert.Equal(zoo.Price, loaded.Price);
        Assert.Equal(zoo.D, loaded.D);
        Assert.Equal(zoo.F, loaded.F);
        Assert.True(loaded.Flag);
        Assert.Equal("hello", loaded.Text);
        Assert.Null(loaded.MaybeText);
        Assert.Equal(zoo.Token, loaded.Token);
        Assert.Equal(zoo.Blob, loaded.Blob);
        Assert.Equal(zoo.At, loaded.At);
        Assert.Equal(DateTimeKind.Utc, loaded.At.Kind);
        Assert.Equal(zoo.AtOffset, loaded.AtOffset);               // same instant
        Assert.Equal(zoo.Day, loaded.Day);
        Assert.Equal(zoo.Clock, loaded.Clock);
        Assert.Equal(TransactionStatus.Completed, loaded.AsText);
        Assert.Equal(TransactionStatus.Cancelled, loaded.AsInt);
        Assert.Null(loaded.MaybeEnum);

        // Storage-representation checks: enum-as-text vs enum-as-int, date as ISO Z.
        Query<EmptyArgs, string> asTextRaw = Query.Inline("select as_text from type_zoo");
        Assert.Equal("Completed", await db.QuerySingleAsync(asTextRaw, EmptyArgs.Value, CancellationToken.None));
        Query<EmptyArgs, long> asIntRaw = Query.Inline("select as_int from type_zoo");
        Assert.Equal((long)TransactionStatus.Cancelled, await db.QuerySingleAsync(asIntRaw, EmptyArgs.Value, CancellationToken.None));
        Query<EmptyArgs, string> atRaw = Query.Inline("select at from type_zoo");
        Assert.EndsWith("Z", await db.QuerySingleAsync(atRaw, EmptyArgs.Value, CancellationToken.None));
    }

    public sealed class AmbiguousDto
    {
        public AmbiguousDto(long id, string name) => (Id, Name) = (id, name);

        public AmbiguousDto(string name, long id) => (Id, Name) = (id, name);

        public long Id { get; }

        public string Name { get; }
    }

    public sealed class RequiredDto
    {
        public long Id { get; set; }

        public required string Name { get; set; }
    }

    public sealed class UriDto
    {
        public Uri? Link { get; set; }
    }

    public sealed record DecimalRow(decimal Amount);

    public sealed record StampRow(DateTime CreatedAt);

    [Fact]
    public async Task Strictness_codes_fire_per_case()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        // MAP-002: entity result missing mapped columns.
        Query<EmptyArgs, User> partial = Query.Inline("select id, name from users");
        Assert.Equal("MAP-002", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(partial, EmptyArgs.Value, CancellationToken.None))).Code);

        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        // MAP-003: two constructors match the same columns.
        Query<EmptyArgs, AmbiguousDto> ambiguous = Query.Inline("select id, name from users");
        Assert.Equal("MAP-003", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(ambiguous, EmptyArgs.Value, CancellationToken.None))).Code);

        // MAP-002: required DTO member without a column.
        Query<EmptyArgs, RequiredDto> missingRequired = Query.Inline("select id from users");
        Assert.Equal("MAP-002", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(missingRequired, EmptyArgs.Value, CancellationToken.None))).Code);

        // MAP-030: no conversion rule and no handler.
        Query<EmptyArgs, UriDto> unhandled = Query.Inline("select 'https://x' as link");
        Assert.Equal("MAP-030", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(unhandled, EmptyArgs.Value, CancellationToken.None))).Code);

        // MAP-031: the rule exists but the value is garbage.
        Query<EmptyArgs, DecimalRow> badDecimal = Query.Inline("select 'abc' as amount");
        Assert.Equal("MAP-031", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(badDecimal, EmptyArgs.Value, CancellationToken.None))).Code);

        // MAP-031: unknown enum name in the column.
        Query<EmptyArgs, Transaction> badEnum = Query.Inline(
            "select 1 as id, 1 as user_id, 'Nope' as status, '1' as amount, 0 as version, "
            + "'2026-01-01T00:00:00Z' as created_at, null as updated_at");
        Assert.Equal("MAP-031", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(badEnum, EmptyArgs.Value, CancellationToken.None))).Code);

        // VAL-020 read: stored datetime without a UTC marker.
        Query<EmptyArgs, StampRow> unmarked = Query.Inline("select '2026-01-01T00:00:00' as created_at");
        Assert.Equal("VAL-020", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(unmarked, EmptyArgs.Value, CancellationToken.None))).Code);
    }

    [Fact]
    public async Task VAL020_rejects_unspecified_kind_on_write()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var user = new User
        {
            Name = "Ada",
            Email = "kind@example.com",
            CreatedAtUtc = new DateTime(2026, 8, 28, 9, 0, 0, DateTimeKind.Unspecified),
        };

        Assert.Equal("VAL-020", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.InsertAsync(user, CancellationToken.None))).Code);
    }

    public readonly record struct Money(decimal Amount);

    private sealed class MoneyHandler : ITypeHandler<Money>
    {
        public Money Parse(object databaseValue)
            => new(Convert.ToDecimal(databaseValue, CultureInfo.InvariantCulture));

        public object Format(Money value)
            => value.Amount.ToString(CultureInfo.InvariantCulture);
    }

    public sealed record PriceRow(Money Price);

    public sealed record PriceArgs(Money P);

    [Fact]
    public async Task Custom_handler_round_trips_both_directions()
    {
        var options = new DbOptions { Dialect = new SqliteDialect() };
        options.TypeHandlers.Register(new MoneyHandler());
        await using var db = await Db.OpenAsync(fixture.ConnectionString, options, CancellationToken.None);

        Query<PriceArgs, PriceRow> echo = Query.Inline("select @P as price");
        var row = await db.QuerySingleAsync(echo, new PriceArgs(new Money(19.99m)), CancellationToken.None);

        Assert.Equal(19.99m, row.Price.Amount);
    }
}
