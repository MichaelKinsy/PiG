package views

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var errUnknownSession = errors.New("unknown session")

func jsonText(value any) ([]byte, error) { return chordjson.Marshal(value) }

func jsonUnmarshal(text []byte, into any) error { return json.Unmarshal(text, into) }

func utf16Units(text string) []uint16 { return utf16.Encode([]rune(text)) }

func unitsPrefix(text string, n int) string { return string(utf16.Decode(utf16Units(text)[:n])) }

func nowMillis() int64 { return time.Now().UnixMilli() }

// newUUID is a random (version 4) UUID, as `crypto.randomUUID()`.
func newUUID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])
}
