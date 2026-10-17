package ai

import (
	"encoding/json"
	"testing"
)

// A payload hook's replacement reaches the SDK as a parsed value (`options.onPayload` returns an object), and the SDK writes it with JSON.stringify
// (openai 7.19.0 internal/request-options.js:29, anthropic client body serialization). PiG holds the replacement as the JSON text an extension
// returned, so it must write what JSON.parse then JSON.stringify gives: escapes decoded, numbers in JavaScript's spelling, a repeated key kept at its first
// position with its last value, and a lone surrogate left escaped.
func TestRawReplacementBodiesAreWrittenAsJSONStringifyWritesThem(t *testing.T) {
	const raw = `{"model":"m","text":"caf\u00e9 \u003cb\u003e","a":1,"n":1.0,"big":1e21,"neg":-0,"a":2,"lone":"\ud800","nested":{"x":"\u00e9","y":[1.50,2e0]}}`
	const want = `{"model":"m","text":"café <b>","a":2,"n":1,"big":1e+21,"neg":0,"lone":"\ud800","nested":{"x":"é","y":[1.5,2]}}`
	t.Run("marshalRequestBody", func(t *testing.T) {
		body, err := marshalRequestBody(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != want {
			t.Errorf("body = %s\nwant   %s", body, want)
		}
	})
	t.Run("anthropicWireBody", func(t *testing.T) {
		body, _, err := anthropicWireBody(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if wantStream := want[:len(want)-1] + `,"stream":true}`; string(body) != wantStream {
			t.Errorf("body = %s\nwant   %s", body, wantStream)
		}
	})
}
