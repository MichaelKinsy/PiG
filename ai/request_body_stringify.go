package ai

import (
	"bytes"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

var replacementCharacter = []byte("\uFFFD")

// marshalStringify is json.Marshal for a request value that may hold a lone UTF-16 surrogate. A Go string holds one as three WTF-8 bytes, which
// encoding/json writes as three U+FFFD characters; JSON.stringify writes the surrogate's own escape. A value with no such byte encodes as before, and only a
// body that has a U+FFFD character (a byte that is not UTF-8 is the usual way to get one) is encoded again with the JSON.stringify encoder, so the common
// request pays for one scan of its output.
func marshalStringify(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(encoded, replacementCharacter) {
		return encoded, nil
	}
	return jsstring.MarshalJSON(value)
}
