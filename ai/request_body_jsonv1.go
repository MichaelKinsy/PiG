//go:build !(go1.27 && goexperiment.jsonv2)

package ai

import (
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// marshalRequestBody is JSON.stringify(body), which Pi's provider SDKs send (openai 7.19.0 internal/request-options.js:29): '<', '>', '&',
// U+2028 and U+2029 as they are, and a lone UTF-16 surrogate as its escape (encoding/json writes U+FFFD for each byte of one).
func marshalRequestBody(body any) ([]byte, error) {
	if canonical, raw, err := rawRequestBody(body); raw {
		return canonical, err
	}
	encoded, err := marshalStringify(body)
	if err != nil {
		return nil, err
	}
	// encoded is the encoder's own copy, so the unescape is written over it instead of into another.
	return jsstring.UnescapeForStringifyInPlace(encoded), nil
}

// encode is the encoder that writes a token where it meets a large string, which this build does not have: it reports false, having done nothing.
func (s *stringSplice) encode(any, func(any) ([]byte, error)) ([]byte, bool, error) {
	return nil, false, nil
}
