package core

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// GUID is the 128-bit identifier behind the `guid` token (§7.9): stored as
// lowercase hyphenated TEXT, read from text or a 16-byte blob. The zero value
// is the empty GUID, which the client-GUID key strategy replaces on insert.
type GUID [16]byte

// NewGUID is a random (version 4) GUID.
func NewGUID() GUID {
	var g GUID
	if _, err := rand.Read(g[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	g[6] = (g[6] & 0x0f) | 0x40
	g[8] = (g[8] & 0x3f) | 0x80
	return g
}

// ParseGUID accepts the hyphenated form (with or without braces) and the 32-hex-digit form, any case.
func ParseGUID(text string) (GUID, error) {
	cleaned := strings.NewReplacer("-", "", "{", "", "}", "").Replace(strings.TrimSpace(text))
	var g GUID
	if len(cleaned) != 32 {
		return g, Errorf("MAP-031", "GUID", "'%s' is not a GUID", text)
	}
	if _, err := hex.Decode(g[:], []byte(cleaned)); err != nil {
		return g, Errorf("MAP-031", "GUID", "'%s' is not a GUID: %s", text, err)
	}
	return g, nil
}

// GUIDFromBytes reads the 16-byte blob form.
func GUIDFromBytes(bytes []byte) (GUID, error) {
	var g GUID
	if len(bytes) != 16 {
		return g, Errorf("MAP-031", "GUID", "a GUID blob has 16 bytes, got %d", len(bytes))
	}
	copy(g[:], bytes)
	return g, nil
}

// String is the lowercase hyphenated form (C#'s "D" format), the stored and exported spelling.
func (g GUID) String() string {
	h := hex.EncodeToString(g[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// IsZero reports the empty GUID.
func (g GUID) IsZero() bool { return g == GUID{} }
