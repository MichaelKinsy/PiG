package experimental

import (
	"bytes"
	"errors"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// canonicalControlJSON mirrors JSON.parse/stringify without losing object insertion order or lone surrogates. Input is validated JSON from encoding/json or the control reader.
func canonicalControlJSON(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("empty control JSON")
	}
	return jsonstringify.Canonicalize(raw)
}
