package ai

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// rawRequestBody is the body of a replacement a payload hook returned as JSON text. Pi's hook returns a parsed value and the SDK JSON.stringify's it, so
// the text is written as JSON.parse then JSON.stringify gives it: escapes decoded, numbers in JavaScript's spelling, a repeated key at its first position with
// its last value.
func rawRequestBody(body any) ([]byte, bool, error) {
	raw, ok := body.(json.RawMessage)
	if !ok {
		return nil, false, nil
	}
	canonical, err := jsonstringify.Canonicalize(raw)
	return canonical, true, err
}
