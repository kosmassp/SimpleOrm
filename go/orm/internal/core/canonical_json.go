package core

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// JSONMember is one key/value pair of a JSONObject, in insertion order.
type JSONMember struct {
	Key   string
	Value any
}

// JSONObject is an insertion-ordered object for CanonicalJSON — Go maps are
// unordered and the pinned artifacts are order-sensitive.
type JSONObject []JSONMember

// Set appends a member (or replaces an existing key in place) and returns the object for chaining.
func (o JSONObject) Set(key string, value any) JSONObject {
	for i := range o {
		if o[i].Key == key {
			o[i].Value = value
			return o
		}
	}
	return append(o, JSONMember{Key: key, Value: value})
}

// CanonicalJSON is the one JSON writer for conformance artifacts (EntityMap
// exports, schema snapshots): byte-identical to the C# reference's
// System.Text.Json output — two-space indent, insertion-ordered keys, `[]` for
// empty lists, and the default STJ escaper (`"` `\` `<` `>` `&` `'` `+` and
// non-ASCII as uppercase \uXXXX; the short escapes for the common controls).
// encoding/json emits lowercase > and sorts map keys, so it cannot produce
// the pinned bytes; two writers would drift (CODING-STANDARD §8).
//
// Values: nil, bool, string, the integer kinds, float64, JSONObject, and any
// slice (a JSON array).
func CanonicalJSON(value any) string {
	var b strings.Builder
	writeJSONValue(&b, value, 0)
	return b.String()
}

func writeJSONValue(b *strings.Builder, value any, depth int) {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeJSONString(b, v)
	case int:
		b.WriteString(strconv.Itoa(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case int32:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case int16:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case uint64:
		b.WriteString(strconv.FormatUint(v, 10))
	case float64:
		b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	case JSONObject:
		writeJSONObject(b, v, depth)
	default:
		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			panic(fmt.Sprintf("cannot write a %T as canonical JSON", value))
		}
		items := make([]any, rv.Len())
		for i := range items {
			items[i] = rv.Index(i).Interface()
		}
		writeJSONArray(b, items, depth)
	}
}

func writeJSONArray(b *strings.Builder, items []any, depth int) {
	if len(items) == 0 {
		b.WriteString("[]")
		return
	}
	b.WriteString("[\n")
	for i, item := range items {
		if i > 0 {
			b.WriteString(",\n")
		}
		writeIndent(b, depth+1)
		writeJSONValue(b, item, depth+1)
	}
	b.WriteByte('\n')
	writeIndent(b, depth)
	b.WriteByte(']')
}

func writeJSONObject(b *strings.Builder, members JSONObject, depth int) {
	if len(members) == 0 {
		b.WriteString("{}")
		return
	}
	b.WriteString("{\n")
	for i, member := range members {
		if i > 0 {
			b.WriteString(",\n")
		}
		writeIndent(b, depth+1)
		writeJSONString(b, member.Key)
		b.WriteString(": ")
		writeJSONValue(b, member.Value, depth+1)
	}
	b.WriteByte('\n')
	writeIndent(b, depth)
	b.WriteByte('}')
}

func writeIndent(b *strings.Builder, depth int) {
	for range depth {
		b.WriteString("  ")
	}
}

// writeJSONString is the STJ default escaper: short escapes for the common
// controls, uppercase \uXXXX (UTF-16 units) for everything else it guards.
func writeJSONString(b *strings.Builder, value string) {
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '"', '<', '>', '&', '\'', '+':
			// The HTML-sensitive set STJ's default encoder guards, as uppercase \u escapes.
			fmt.Fprintf(b, `\u%04X`, r)
		default:
			switch {
			case r < 0x20 || r == 0x7F:
				fmt.Fprintf(b, `\u%04X`, r)
			case r < utf8.RuneSelf:
				b.WriteRune(r)
			case r > 0xFFFF:
				high, low := utf16.EncodeRune(r)
				fmt.Fprintf(b, `\u%04X\u%04X`, high, low)
			default:
				fmt.Fprintf(b, `\u%04X`, r)
			}
		}
	}
	b.WriteByte('"')
}
