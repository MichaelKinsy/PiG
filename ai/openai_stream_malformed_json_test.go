package ai

import (
	"strings"
	"testing"
)

// openai 7.19.0 (Pi 1.1.0's SDK) core/streaming.ts Stream.fromSSEResponse: a record whose data is not JSON throws
// SyntaxError("Error reading response: malformed server-sent event JSON."), not the JSON.parse error. Probed: Pi 1.1.0 ends an
// openai-responses (gpt-4o) and an openai-completions (groq) stream with that errorMessage; PiG gave Go's "invalid character 'b'
// looking for beginning of object key string".
func TestOpenAIStreamMalformedRecordIsTheSDKsSyntaxErrorText(t *testing.T) {
	decoder := newOpenAIStreamDecoder(strings.NewReader("data: {\"ok\":1}\n\ndata: {bad json\n\n"), nil, nil)
	if !decoder.Next() {
		t.Fatalf("first record: %v", decoder.Err())
	}
	if decoder.Next() {
		t.Fatal("a malformed record was accepted")
	}
	if got, want := decoder.Err().Error(), "Error reading response: malformed server-sent event JSON."; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

// fromSSEResponse throws `new APIError(undefined, data?.error ?? data)` for a record whose event is "error", before the provider sees
// it, so Pi's errorMessage is the record's message. Probed (Pi 1.1.0, openai-responses): `event: error` with
// {"type":"error","code":"rate_limit_exceeded","message":"Rate limit reached"} gives "Rate limit reached"; PiG passed the record to
// processResponsesStream, whose error branch gave "Error Code rate_limit_exceeded: Rate limit reached".
func TestOpenAIStreamErrorEventIsTheSDKsAPIError(t *testing.T) {
	for _, tc := range []struct{ data, want string }{
		{`{"type":"error","code":"rate_limit_exceeded","message":"Rate limit reached","param":null}`, "Rate limit reached"},
		{`{"error":{"message":"inner"},"message":"outer"}`, "inner"},
		{`{"error":null,"code":5}`, `{"error":null,"code":5}`},
		{`{"type":"error","message":{"text":"x"}}`, `{"text":"x"}`},
		// APIError.makeMessage (openai core/error.js:20-38) over `data?.error ?? data`, checked with the oracle's openai 7.19.0: a falsy
		// error member or a null record has no message, and any other value is JSON.stringify's compact text.
		{`{"error":false}`, "(no status code or body)"},
		{`null`, "(no status code or body)"},
		{`"str"`, `"str"`},
		{`[1, 2]`, `[1,2]`},
		{`{"error":{"message": {"a": 1}}}`, `{"a":1}`},
		{`{"code": 5 , "x" : [1, 2]}`, `{"code":5,"x":[1,2]}`},
	} {
		decoder := newOpenAIStreamDecoder(strings.NewReader("data: {\"ok\":1}\n\nevent: error\ndata: "+tc.data+"\n\n"), nil, nil)
		if !decoder.Next() {
			t.Fatalf("first record: %v", decoder.Err())
		}
		if decoder.Next() {
			t.Fatalf("%s: the error event was delivered as a record", tc.data)
		}
		if decoder.Err() == nil || decoder.Err().Error() != tc.want {
			t.Fatalf("%s: error = %v, want %q", tc.data, decoder.Err(), tc.want)
		}
	}
}
