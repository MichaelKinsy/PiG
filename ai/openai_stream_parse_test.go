package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// legacyOpenAIStreamParse is parse as it decoded every record into a RawMessage and its members into a map.
func legacyOpenAIStreamParse(sse *serverSentEvent) error {
	var data json.RawMessage
	if err := json.Unmarshal([]byte(sse.Data), &data); err != nil {
		return &openAIStreamDecodeError{SSE: *sse, Cause: err}
	}
	if len(sse.Event) >= 7 && sse.Event[:7] == "thread." {
		wrapped, err := json.Marshal(struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}{sse.Event, data})
		if err != nil {
			return err
		}
		sse.Data = string(wrapped)
		return nil
	}
	var object map[string]json.RawMessage
	if len(data) > 0 && data[0] == '{' {
		if err := json.Unmarshal(data, &object); err != nil {
			return err
		}
	}
	if sse.Event == "error" {
		// `data?.error ?? data`, then makeMessage's fallback for a falsy value.
		value := object["error"]
		if value == nil || string(value) == "null" {
			value = data
		}
		cause := openAIStreamAPIError(value)
		if cause == nil {
			cause = errors.New("(no status code or body)")
		}
		return &openAIStreamDecodeError{SSE: *sse, APIError: value, Cause: cause}
	}
	if err := openAIStreamAPIError(object["error"]); err != nil {
		return &openAIStreamDecodeError{SSE: *sse, APIError: object["error"], Cause: err}
	}
	return nil
}

// The record check reads the "error" member only when the text can contain it, and gives the same verdict, error text and rewritten data as the full decode for every kind of record.
func TestOpenAIStreamParseMatchesFullDecode(t *testing.T) {
	records := []string{
		``, ` `, `null`, `0`, `1e400`, `"text"`, `[]`, `[{"error":"x"}]`, `{}`, `{ }`, `{"a":1}`,
		`{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"content":"hi"}}]}`,
		`{"error":{"message":"boom","type":"server_error"}}`, `{"error":"plain"}`, `{"error":null}`, `{"error":false}`, `{"error":0}`, `{"error":{}}`, `{"error":[]}`, `{"error":{"message":""}}`,
		`{"a":1,"error":{"message":"later"}}`, `{"error":"first","error":"last"}`, `{"error":"first","error":null}`,
		`{"\u0065rror":"escaped key"}`, `{"err\u006fr":{"message":"escaped"}}`, `{"Error":"case"}`, `{"ERROR":{"message":"upper"}}`,
		`{"choices":[{"delta":{"content":"error in the text"}}]}`, `{"choices":[{"delta":{"content":"a\nb"}}]}`,
		`{"msg":"\u00e9","error":{"message":"é"}}`, "{\"a\":\"\xff\"}", "\xef\xbb\xbf{}", `{"a":`, `{"a":1}}`, `{"a" 1}`, `tru`, `{"error":{"message":"x"}`,
		`{"n":1e400}`, `{"n":-0}`, `{"nested":{"error":"inner"}}`,
		" {\"error\":{\"message\":\"leading space\"}}", "\n{\"error\":\"after a blank data line\"}", "\t\r\n {\"a\":1}", "{\"error\":\"trailing\"} \n",
	}
	for _, event := range []string{"", "message", "thread.run.created", "error"} {
		for _, record := range records {
			got := serverSentEvent{Event: event, Data: record}
			want := serverSentEvent{Event: event, Data: record}
			gotErr := (&openAIStreamDecoder{}).parse(&got)
			wantErr := legacyOpenAIStreamParse(&want)
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || (gotErr == nil) != (wantErr == nil) || got.Data != want.Data {
				t.Errorf("event %q record %q:\n got  %v, data %q\n want %v, data %q", event, record, gotErr, got.Data, wantErr, want.Data)
			}
		}
	}
}
