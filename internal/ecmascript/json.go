// Package ecmascript preserves JavaScript JSON and string semantics at native boundaries.
package ecmascript

import (
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// CanonicalJSON validates input before applying JSON.parse/stringify semantics, preserving source key order and UTF-16 code units.
func CanonicalJSON(raw []byte) ([]byte, error) {
	if !json.Valid(raw) {
		return nil, errors.New("invalid JSON")
	}
	return jsonstringify.Canonicalize(raw)
}
