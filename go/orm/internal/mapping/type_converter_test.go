package mapping_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

func newConverter() *mapping.TypeConverter {
	return mapping.NewTypeConverter(core.NewTypeHandlerRegistry(), false)
}

func TestTypeConverter_IntegersRoundTrip(t *testing.T) {
	c := newConverter()
	if got, err := c.FromDatabase(int64(42), reflect.TypeFor[int64](), core.TypeInt64, "ctx"); err != nil || got != int64(42) {
		t.Errorf("int64 from int64: %v %v", got, err)
	}
	if got, err := c.FromDatabase("42", reflect.TypeFor[int64](), core.TypeInt64, "ctx"); err != nil || got != int64(42) {
		t.Errorf("int64 from string: %v %v", got, err)
	}
	if got, err := c.FromDatabase(7.0, reflect.TypeFor[int32](), core.TypeInt32, "ctx"); err != nil || got != int32(7) {
		t.Errorf("int32 from float64: %v %v", got, err)
	}
	if got, err := c.ToDatabase(int64(42), "", "ctx"); err != nil || got != int64(42) {
		t.Errorf("toDatabase int64: %v %v", got, err)
	}
}

func TestTypeConverter_Int32Overflow_IsMAP031(t *testing.T) {
	c := newConverter()
	if _, err := c.FromDatabase(int64(2147483648), reflect.TypeFor[int32](), core.TypeInt32, "ctx"); core.CodeOf(err) != "MAP-031" {
		t.Errorf("expected MAP-031, got %v", err)
	}
}

func TestTypeConverter_Int16Overflow_IsMAP031(t *testing.T) {
	c := newConverter()
	if _, err := c.FromDatabase(int64(40000), reflect.TypeFor[int16](), core.TypeInt16, "ctx"); core.CodeOf(err) != "MAP-031" {
		t.Errorf("expected MAP-031, got %v", err)
	}
}

func TestTypeConverter_DecimalRoundTripsAsCanonicalText(t *testing.T) {
	c := newConverter()
	got, err := c.FromDatabase("1234.56", reflect.TypeFor[core.Decimal](), core.TypeDecimal, "ctx")
	if err != nil {
		t.Fatal(err)
	}
	d, ok := got.(core.Decimal)
	if !ok || !d.Equal(core.MustDecimal("1234.56")) {
		t.Errorf("decimal from string: %v", got)
	}
	if stored, err := c.ToDatabase(core.MustDecimal("1234.56"), "", "ctx"); err != nil || stored != "1234.56" {
		t.Errorf("decimal to database: %v %v", stored, err)
	}
}

func TestTypeConverter_DecimalAcceptsIntegerAndRealStorageToo(t *testing.T) {
	c := newConverter()
	fromInt, err := c.FromDatabase(int64(5), reflect.TypeFor[core.Decimal](), core.TypeDecimal, "ctx")
	if err != nil || !fromInt.(core.Decimal).Equal(core.MustDecimal("5")) {
		t.Errorf("decimal from int64: %v %v", fromInt, err)
	}
	fromFloat, err := c.FromDatabase(2.5, reflect.TypeFor[core.Decimal](), core.TypeDecimal, "ctx")
	if err != nil || !fromFloat.(core.Decimal).Equal(core.MustDecimal("2.5")) {
		t.Errorf("decimal from float64: %v %v", fromFloat, err)
	}
}

func TestTypeConverter_DoubleAndFloatRoundTrip(t *testing.T) {
	c := newConverter()
	if got, err := c.FromDatabase(2.25, reflect.TypeFor[float64](), core.TypeDouble, "ctx"); err != nil || got != 2.25 {
		t.Errorf("double: %v %v", got, err)
	}
	if got, err := c.FromDatabase("1.5", reflect.TypeFor[float32](), core.TypeFloat, "ctx"); err != nil || got != float32(1.5) {
		t.Errorf("float from string: %v %v", got, err)
	}
	if got, err := c.ToDatabase(2.25, "", "ctx"); err != nil || got != 2.25 {
		t.Errorf("toDatabase float64: %v %v", got, err)
	}
}

func TestTypeConverter_BoolRoundTripsThroughIntegerZeroOne(t *testing.T) {
	c := newConverter()
	if got, err := c.FromDatabase(int64(1), reflect.TypeFor[bool](), core.TypeBool, "ctx"); err != nil || got != true {
		t.Errorf("bool from 1: %v %v", got, err)
	}
	if got, err := c.FromDatabase(int64(0), reflect.TypeFor[bool](), core.TypeBool, "ctx"); err != nil || got != false {
		t.Errorf("bool from 0: %v %v", got, err)
	}
	if got, err := c.ToDatabase(true, "", "ctx"); err != nil || got != true {
		t.Errorf("toDatabase true: %v %v", got, err)
	}
}

func TestTypeConverter_StringRoundTripsUntouched(t *testing.T) {
	c := newConverter()
	value := "O'Brien; drop table users; --"
	if got, err := c.FromDatabase(value, reflect.TypeFor[string](), core.TypeString, "ctx"); err != nil || got != value {
		t.Errorf("string round trip: %v %v", got, err)
	}
	if got, err := c.ToDatabase(value, "", "ctx"); err != nil || got != value {
		t.Errorf("toDatabase string: %v %v", got, err)
	}
}

func TestTypeConverter_GuidNormalizesToLowercaseCanonicalText(t *testing.T) {
	c := newConverter()
	got, err := c.FromDatabase("6F9619FF-8B86-D011-B42D-00CF4FC964FF", reflect.TypeFor[core.GUID](), core.TypeGUID, "ctx")
	if err != nil {
		t.Fatal(err)
	}
	if got.(core.GUID).String() != "6f9619ff-8b86-d011-b42d-00cf4fc964ff" {
		t.Errorf("guid normalization: %v", got)
	}
}

func TestTypeConverter_GuidAcceptsSixteenByteBinaryForm(t *testing.T) {
	c := newConverter()
	g, _ := core.ParseGUID("6f9619ff-8b86-d011-b42d-00cf4fc964ff")
	got, err := c.FromDatabase(g[:], reflect.TypeFor[core.GUID](), core.TypeGUID, "ctx")
	if err != nil || got.(core.GUID) != g {
		t.Errorf("guid from blob: %v %v", got, err)
	}
}

func TestTypeConverter_GuidTypeOverrideTargetsAPlainString(t *testing.T) {
	// CODING-STANDARD §10: `type=guid` on a string field normalizes the text
	// but keeps the Go type a string, not core.GUID.
	c := newConverter()
	got, err := c.FromDatabase("6F9619FF-8B86-D011-B42D-00CF4FC964FF", reflect.TypeFor[string](), core.TypeGUID, "ctx")
	if err != nil || got != "6f9619ff-8b86-d011-b42d-00cf4fc964ff" {
		t.Errorf("guid onto string target: %v %v", got, err)
	}
}

func TestTypeConverter_BytesRoundTripAsRawBinary(t *testing.T) {
	c := newConverter()
	blob := []byte{1, 2, 3}
	got, err := c.FromDatabase(blob, reflect.TypeFor[[]byte](), core.TypeBytes, "ctx")
	if err != nil || string(got.([]byte)) != string(blob) {
		t.Errorf("bytes round trip: %v %v", got, err)
	}
	if stored, err := c.ToDatabase(blob, "", "ctx"); err != nil || string(stored.([]byte)) != string(blob) {
		t.Errorf("toDatabase bytes: %v %v", stored, err)
	}
}

func TestTypeConverter_DatetimeReadsUTCMarkerAndNormalizesToUTC(t *testing.T) {
	c := newConverter()
	got, err := c.FromDatabase("2026-08-28T10:00:00.0000000Z", reflect.TypeFor[time.Time](), core.TypeDateTime, "ctx")
	if err != nil {
		t.Fatal(err)
	}
	value := got.(time.Time)
	if value.Location() != time.UTC {
		t.Errorf("not UTC: %v", value.Location())
	}
	if value.Format("2006-01-02T15:04:05") != "2026-08-28T10:00:00" {
		t.Errorf("wrong instant: %v", value)
	}
}

func TestTypeConverter_DatetimeReadsAnExplicitOffsetAndNormalizesToUTC(t *testing.T) {
	c := newConverter()
	got, err := c.FromDatabase("2026-08-28T12:00:00+02:00", reflect.TypeFor[time.Time](), core.TypeDateTime, "ctx")
	if err != nil {
		t.Fatal(err)
	}
	if got.(time.Time).Format("2006-01-02T15:04:05") != "2026-08-28T10:00:00" {
		t.Errorf("wrong instant: %v", got)
	}
}

func TestTypeConverter_DatetimeWithoutMarkerIsVAL020OnRead(t *testing.T) {
	c := newConverter()
	if _, err := c.FromDatabase("2026-01-01T00:00:00", reflect.TypeFor[time.Time](), core.TypeDateTime, "ctx"); core.CodeOf(err) != "VAL-020" {
		t.Errorf("expected VAL-020, got %v", err)
	}
}

func TestTypeConverter_DatetimeWritesSevenFractionalDigitsAndTrailingZ(t *testing.T) {
	c := newConverter()
	value := time.Date(2026, 8, 28, 10, 0, 0, 500000000, time.UTC)
	got, err := c.ToDatabase(value, "", "ctx")
	if err != nil || got != "2026-08-28T10:00:00.5000000Z" {
		t.Errorf("got %v %v", got, err)
	}
}

func TestTypeConverter_DatetimeWritesConvertNonUTCZoneToUTC(t *testing.T) {
	c := newConverter()
	zone := time.FixedZone("+02:00", 2*60*60)
	value := time.Date(2026, 8, 28, 12, 0, 0, 0, zone)
	got, err := c.ToDatabase(value, "", "ctx")
	if err != nil || got != "2026-08-28T10:00:00.0000000Z" {
		t.Errorf("got %v %v", got, err)
	}
}

func TestTypeConverter_DateAndTimeTokensParseTheirOwnFormat(t *testing.T) {
	c := newConverter()
	date, err := c.FromDatabase("2026-08-28", reflect.TypeFor[time.Time](), core.TypeDate, "ctx")
	if err != nil || date.(time.Time).Format("2006-01-02") != "2026-08-28" {
		t.Errorf("date: %v %v", date, err)
	}
	clock, err := c.FromDatabase("13:30:15", reflect.TypeFor[time.Time](), core.TypeTime, "ctx")
	if err != nil || clock.(time.Time).Format("15:04:05") != "13:30:15" {
		t.Errorf("time: %v %v", clock, err)
	}
}

func TestTypeConverter_DatetimeOffsetKeepsItsOwnOffset(t *testing.T) {
	c := newConverter()
	got, err := c.FromDatabase("2026-08-28T12:00:00+02:00", reflect.TypeFor[time.Time](), core.TypeDateTimeOffset, "ctx")
	if err != nil {
		t.Fatal(err)
	}
	_, offset := got.(time.Time).Zone()
	if offset != 2*60*60 {
		t.Errorf("offset not kept: %v", got)
	}
	value := time.Date(2026, 8, 28, 12, 0, 0, 0, time.FixedZone("+02:00", 2*60*60))
	stored, err := c.ToDatabase(value, core.TypeDateTimeOffset, "ctx")
	if err != nil || stored != "2026-08-28T12:00:00.0000000+02:00" {
		t.Errorf("stored offset: %v %v", stored, err)
	}
}

func TestTypeConverter_EnumTextMatchesCaseInsensitivelyByName(t *testing.T) {
	c := newConverter()
	got, err := c.FromDatabase("completed", reflect.TypeFor[sample.TransactionStatus](), core.TypeEnumText, "ctx")
	if err != nil || got != sample.Completed {
		t.Errorf("enum from name: %v %v", got, err)
	}
	stored, err := c.ToDatabase(sample.Cancelled, "", "ctx")
	if err != nil || stored != "Cancelled" {
		t.Errorf("enum to database: %v %v", stored, err)
	}
}

func TestTypeConverter_EnumTextUnknownNameIsMAP031(t *testing.T) {
	c := newConverter()
	if _, err := c.FromDatabase("Nope", reflect.TypeFor[sample.TransactionStatus](), core.TypeEnumText, "ctx"); core.CodeOf(err) != "MAP-031" {
		t.Errorf("expected MAP-031, got %v", err)
	}
}

func TestTypeConverter_EnumIntReadsByOrdinalAndWritesTheOrdinal(t *testing.T) {
	c := newConverter()
	got, err := c.FromDatabase(int64(2), reflect.TypeFor[sample.TransactionStatus](), core.TypeEnumInt, "ctx")
	if err != nil || got != sample.Cancelled {
		t.Errorf("enum from ordinal: %v %v", got, err)
	}
	stored, err := c.ToDatabase(sample.Cancelled, core.TypeEnumInt, "ctx")
	if err != nil || stored != int64(2) {
		t.Errorf("enum ordinal to database: %v %v", stored, err)
	}
}

func TestTypeConverter_EnumIntOutOfRangeOrdinalIsMAP031(t *testing.T) {
	c := newConverter()
	if _, err := c.FromDatabase(int64(99), reflect.TypeFor[sample.TransactionStatus](), core.TypeEnumInt, "ctx"); core.CodeOf(err) != "MAP-031" {
		t.Errorf("expected MAP-031, got %v", err)
	}
}

func TestTypeConverter_CustomColumnTypeWithNoHandlerIsMAP030(t *testing.T) {
	c := newConverter()
	if _, err := c.FromDatabase("x", reflect.TypeFor[struct{ X int }](), core.TypeCustom, "ctx"); core.CodeOf(err) != "MAP-030" {
		t.Errorf("expected MAP-030, got %v", err)
	}
}

func TestTypeConverter_AnUnstorableGoValueIsMAP030OnWrite(t *testing.T) {
	c := newConverter()
	if _, err := c.ToDatabase(struct{ X int }{1}, "", "ctx"); core.CodeOf(err) != "MAP-030" {
		t.Errorf("expected MAP-030, got %v", err)
	}
}

type money struct{ amount float64 }

type moneyHandler struct{}

func (moneyHandler) Parse(databaseValue any) (money, error) {
	f, ok := databaseValue.(float64)
	if !ok {
		return money{}, core.Errorf("MAP-031", "money", "cannot parse %T", databaseValue)
	}
	return money{amount: f}, nil
}

func (moneyHandler) Format(value money) (any, error) {
	return value.amount, nil
}

func TestTypeConverter_ARegisteredHandlerWinsOverTheFixedTable(t *testing.T) {
	registry := core.NewTypeHandlerRegistry()
	core.RegisterHandler[money](registry, moneyHandler{})
	c := mapping.NewTypeConverter(registry, false)

	got, err := c.FromDatabase(19.99, reflect.TypeFor[money](), core.TypeCustom, "ctx")
	if err != nil || got.(money).amount != 19.99 {
		t.Errorf("handler parse: %v %v", got, err)
	}
	stored, err := c.ToDatabase(money{amount: 19.99}, "", "ctx")
	if err != nil || stored != 19.99 {
		t.Errorf("handler format: %v %v", stored, err)
	}
	if !c.HasHandler(reflect.TypeFor[money]()) {
		t.Error("HasHandler should see the registration")
	}
}

func TestTypeConverter_NullPassesThroughBothDirections(t *testing.T) {
	c := newConverter()
	if got, err := c.FromDatabase(nil, reflect.TypeFor[*string](), core.TypeString, "ctx"); err != nil || got != nil {
		t.Errorf("nil into pointer target: %v %v", got, err)
	}
	if _, err := c.FromDatabase(nil, reflect.TypeFor[string](), core.TypeString, "ctx"); core.CodeOf(err) != "MAP-031" {
		t.Errorf("nil into non-pointer target is MAP-031, got %v", err)
	}
	if got, err := c.ToDatabase(nil, "", "ctx"); err != nil || got != nil {
		t.Errorf("nil to database: %v %v", got, err)
	}
}
