package views

import (
	"encoding/json"
	"errors"
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
