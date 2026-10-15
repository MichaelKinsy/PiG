package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// anthropicSSEStreams returns seeded response bodies for Pi's iterateAnthropicEvents, split into chunks at arbitrary bytes.
func anthropicSSEStreams() [][][]byte {
	random := rand.New(rand.NewSource(20261103))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	var streams [][][]byte
	for range 6000 {
		var body strings.Builder
		if random.Intn(25) == 0 {
			body.WriteString("\ufeff")
		}
		eol := func() string { return pick("\n", "\n", "\n", "\r\n", "\r") }
		for range 1 + random.Intn(6) {
			for range random.Intn(3) {
				body.WriteString(pick(": ping", "id: 3", "retry: 5", "foo: bar", "data", "event") + eol())
			}
			event := pick("message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "ping", "error", "message_start", "content_block_delta", "", "x")
			if event != "" {
				body.WriteString(pick("event: ", "event:", "event: ") + event + eol())
			}
			for range random.Intn(3) {
				data := pick(`{"type":"message_start","message":{"id":"m1"}}`, `{"type":"message_stop"}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"é𝄞"}}`,
					`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`, `{"type":"content_block_stop","index":0}`, `{"type":"ping"}`, `{"TYPE":"message_start"}`, `{"type":"message_stop","Type":"x"}`, `{"type":["message_start"]}`,
					`{"type":"message_start"`, `{"type":"content_block_delta","delta":{"text":"a\nb`, `{"a":1,}`, `[1,2,]`, `not json`, ``, `null`, `7`, `"s"`,
					`{"type":"message_start"}{"x":1}`, `{"text":"tab	in string"}`, "{\"text\":\"\xff\"}", `{"type":"message_stop"} `, `boom`, `{"error":{"type":"overloaded_error"}}`, `{"a":"\u00e9\ud83d"}`, `{"a":"\x"}`)
				body.WriteString(pick("data: ", "data:", "data:  ") + data + eol())
			}
			if random.Intn(8) != 0 {
				body.WriteString(eol())
			}
		}
		raw := []byte(body.String())
		if random.Intn(40) == 0 && len(raw) > 0 {
			raw = raw[:random.Intn(len(raw))]
		}
		var chunks [][]byte
		for len(raw) > 0 {
			size := 1 + random.Intn(max(len(raw), 1))
			if random.Intn(3) == 0 {
				size = 1 + random.Intn(4)
			}
			size = min(size, len(raw))
			// pig divergence (D121): a CR at the end of a chunk waits for the next chunk, where Pi ends the line and reads the LF that follows as an empty line. The probe never splits a CRLF.
			if size < len(raw) && raw[size-1] == '\r' && raw[size] == '\n' {
				size++
			}
			chunks = append(chunks, raw[:size])
			raw = raw[size:]
		}
		streams = append(streams, chunks)
	}
	return streams
}

// TestAnthropicEventReaderMatchesPi runs Pi's iterateAnthropicEvents (the installed pi-ai dist) and Pig's event reader over the same chunked bodies and compares the parsed events and the way the stream ends.
func TestAnthropicEventReaderMatchesPi(t *testing.T) {
	streams := anthropicSSEStreams()
	cases := make([]sseOracleCase, len(streams))
	for i, chunks := range streams {
		cases[i].Chunks = []string{}
		for _, chunk := range chunks {
			cases[i].Chunks = append(cases[i].Chunks, base64.StdEncoding.EncodeToString(chunk))
		}
	}
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/anthropic_sse_oracle.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%.2000s", err, &stderr)
	}
	var expected []struct {
		Events []json.RawMessage `json:"events"`
		Error  *string           `json:"error"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	canonical := func(raw []byte) any {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("not JSON: %q", raw)
		}
		return value
	}
	differing, events, failures := 0, 0, 0
	for i, chunks := range streams {
		reader := newAnthropicEventReader(context.Background(), &anthropicReaderSource{reader: &sseChunkReader{chunks: cloneChunks(chunks)}}, func() {})
		var got []any
		var gotError *string
		for {
			event, ok, err := reader.next()
			if err != nil {
				text := err.Error()
				gotError = &text
				break
			}
			if !ok {
				break
			}
			parsed, _, perr := anthropicParseJSON(event.Data)
			if perr != nil {
				t.Fatalf("a yielded event does not parse: %v", perr)
			}
			got = append(got, canonical([]byte(parsed)))
		}
		var want []any
		for _, raw := range expected[i].Events {
			want = append(want, canonical(raw))
		}
		events += len(want)
		if expected[i].Error != nil {
			failures++
		}
		problem := ""
		if len(got) != len(want) || (len(got) > 0 && !reflect.DeepEqual(got, want)) {
			problem = "events differ"
		} else if (gotError == nil) != (expected[i].Error == nil) || (gotError != nil && *gotError != *expected[i].Error) {
			problem = "ending differs"
		}
		if problem != "" {
			differing++
			if differing <= 5 {
				var body []byte
				for _, c := range chunks {
					body = append(body, c...)
				}
				wantError := "<nil>"
				if expected[i].Error != nil {
					wantError = *expected[i].Error
				}
				gotText := "<nil>"
				if gotError != nil {
					gotText = *gotError
				}
				t.Errorf("case %d: %s\n  body %q\n  got  %v / %.200s\n  want %v / %.200s", i, problem, body, got, gotText, want, wantError)
			}
		}
	}
	t.Logf("%d streams, %d events, %d failed streams, %d differing", len(streams), events, failures, differing)
	if differing > 0 {
		t.Fatalf("%d streams differ from Pi", differing)
	}
}

// TestAnthropicEventTypeReadsTheExactLastTopLevelMember: `event.type` after JSON.parse (anthropic-messages.ts:551-556) names the last top-level member spelled exactly "type", after unescaping, and is a type only when it is a string.
func TestAnthropicEventTypeReadsTheExactLastTopLevelMember(t *testing.T) {
	for _, test := range []struct{ data, want string }{
		{`{"type":"message_start"}`, "message_start"},
		{` { "type" : "message_stop" } `, "message_stop"},
		{`{"message":{"type":"message"},"type":"message_start"}`, "message_start"},
		{`{"delta":{"type":"text_delta"},"index":0}`, ""},
		{`{"a":["type",{"type":"x"}],"b":"\"type\"","type":"ping"}`, "ping"},
		{`{"type":"message_start","type":"message_stop"}`, "message_stop"},
		{`{"type":"message_start","type":["message_stop"]}`, ""},
		{`{"TYPE":"message_start"}`, ""},
		{`{"type":"a","Type":"message_start"}`, "a"},
		{`{"\u0074ype":"message_start"}`, "message_start"},
		{`{"type":"message\u005fstart"}`, "message_start"},
		{`{"type":7}`, ""}, {`{"type":null}`, ""}, {`{}`, ""}, {`[{"type":"x"}]`, ""}, {`"type"`, ""}, {`7`, ""},
		{`{"a":1.5e3,"b":true,"c":null,"type":"message_delta"}`, "message_delta"},
	} {
		if got := anthropicEventType(test.data); got != test.want {
			t.Errorf("anthropicEventType(%s) = %q, want %q", test.data, got, test.want)
		}
	}
}
