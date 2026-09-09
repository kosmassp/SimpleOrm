package core

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// CompareValues orders two key or foreign-key values of the same underlying
// type — both always come from the same mapped property (§7.4,
// spec/loading.md: "ordered by target key, compared as values, never as a
// string rendering" — ADR-0021 add.1's example, "10 comes after 2", is what a
// naive string sort gets wrong). This is the one comparator KeyTuple.Compare
// and every merged-chunk/collection sort in db_eager.go and db_eager_join.go
// uses, covering the fixed table of types a declared key or foreign key can
// realistically hold: signed and unsigned integers (compared as 64-bit,
// never truncated), floats, strings, bools, time.Time, GUID (lexicographic
// over its 16 bytes — the value ordering for a fixed-width identifier, not a
// stringified rendering), and Decimal (arbitrary-precision numeric via
// Decimal.Compare, never Decimal.String() — "19.9" must not sort after
// "19.90" by digit count). Anything else falls back to comparing formatted
// text, which is not truly value-wise; no fixture or sample key needs it
// today.
func CompareValues(a, b any) int {
	switch av := a.(type) {
	case nil:
		if b == nil {
			return 0
		}
		return -1
	case int:
		return compareOrdered(int64(av), int64(b.(int)))
	case int8:
		return compareOrdered(int64(av), int64(b.(int8)))
	case int16:
		return compareOrdered(int64(av), int64(b.(int16)))
	case int32:
		return compareOrdered(int64(av), int64(b.(int32)))
	case int64:
		return compareOrdered(av, b.(int64))
	case uint:
		return compareOrdered(uint64(av), uint64(b.(uint)))
	case uint8:
		return compareOrdered(uint64(av), uint64(b.(uint8)))
	case uint16:
		return compareOrdered(uint64(av), uint64(b.(uint16)))
	case uint32:
		return compareOrdered(uint64(av), uint64(b.(uint32)))
	case uint64:
		return compareOrdered(av, b.(uint64))
	case float32:
		return compareOrdered(float64(av), float64(b.(float32)))
	case float64:
		return compareOrdered(av, b.(float64))
	case string:
		return strings.Compare(av, b.(string))
	case bool:
		bv := b.(bool)
		switch {
		case av == bv:
			return 0
		case !av:
			return -1
		default:
			return 1
		}
	case time.Time:
		bv := b.(time.Time)
		switch {
		case av.Before(bv):
			return -1
		case av.After(bv):
			return 1
		default:
			return 0
		}
	case GUID:
		bv := b.(GUID)
		return bytes.Compare(av[:], bv[:])
	case Decimal:
		return av.Compare(b.(Decimal))
	default:
		if b == nil {
			return 1
		}
		return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
	}
}

type ordered interface {
	~int64 | ~uint64 | ~float64
}

func compareOrdered[T ordered](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
