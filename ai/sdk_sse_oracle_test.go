package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type sseOracleCase struct {
	Chunks []string `json:"chunks"`
}

type sseOracleResult struct {
	Items []json.RawMessage `json:"items"`
	Error *struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"error"`
}

// sseOracleStreams returns seeded response bodies, each split into chunks at arbitrary bytes: event streams with every line ending, comment lines, fields with and without the optional space, repeated data, [DONE], JSON errors, malformed JSON, a byte order mark, invalid UTF-8 and an unterminated last event.
func sseOracleStreams() [][][]byte {
	random := rand.New(rand.NewSource(20261102))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	var streams [][][]byte
	for range 6000 {
		var body strings.Builder
		if random.Intn(20) == 0 {
			body.WriteString("\ufeff")
		}
		eol := func() string { return pick("\n", "\n", "\n", "\r\n", "\r", "\r\n") }
		for range 1 + random.Intn(6) {
			for range random.Intn(4) {
				switch random.Intn(9) {
				case 0:
					body.WriteString(": comment" + eol())
				case 1:
					body.WriteString("event: " + pick("message", "thread.run.created", "error", "ping", "", "x y") + eol())
				case 2:
					body.WriteString("event:" + pick("message", "thread.message.delta", "error") + eol())
				case 3:
					body.WriteString("id: 7" + eol())
				case 4:
					body.WriteString("retry: 100" + eol())
				case 5:
					body.WriteString("data" + eol())
				case 6:
					body.WriteString("data : spaced" + eol())
				case 7:
					body.WriteString(pick("\x00junk", "foo: bar", ":", "  data: indented") + eol())
				default:
					body.WriteString("event" + eol())
				}
			}
			for range random.Intn(3) {
				value := pick(`{"a":1}`, `{"id":"x","choices":[{"delta":{"content":"é𝄞"}}]}`, `{"error":{"message":"boom"}}`, `{"error":"plain"}`, `{"error":{"message":""}}`, `{"error":0}`, `{"error":null}`,
					`[1,2]`, `"str"`, `7`, `null`, `[DONE]`, `[DONE] tail`, `{"a":`, `not json`, ``, ` {"a":2} `, `{"a":1}{"b":2}`, "{\"a\":\"\xff\"}", `{"error":{"message":{"deep":[1]}}}`, `{"error":{"code":"x"}}`)
				prefix := pick("data: ", "data: ", "data:", "data:  ")
				body.WriteString(prefix + value + eol())
				if random.Intn(8) == 0 {
					body.WriteString("data: " + pick(`{"b":`, `3}`, `x`) + eol())
				}
			}
			if random.Intn(10) != 0 {
				body.WriteString(eol())
				if random.Intn(8) == 0 {
					body.WriteString(eol())
				}
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
			chunks = append(chunks, raw[:size])
			raw = raw[size:]
		}
		streams = append(streams, chunks)
	}
	return streams
}

// TestOpenAIStreamDecoderMatchesSDK runs the pinned openai SDK's Stream.fromSSEResponse and Pig's decoder over the same chunked bodies and compares the parsed items and the way the stream ends.
func TestOpenAIStreamDecoderMatchesSDK(t *testing.T) {
	streams := sseOracleStreams()
	cases := make([]sseOracleCase, len(streams))
	for i, chunks := range streams {
		for _, chunk := range chunks {
			cases[i].Chunks = append(cases[i].Chunks, base64.StdEncoding.EncodeToString(chunk))
		}
		if cases[i].Chunks == nil {
			cases[i].Chunks = []string{}
		}
	}
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", filepath.Join(testenv.ModuleRoot(t), "ai", "testdata", "sdk_sse_oracle.mjs"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi SDK oracle (npm ci --ignore-scripts in ai/testdata/sdk first): %v\n%.2000s", err, &stderr)
	}
	var expected []sseOracleResult
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
	differing, items, apiErrors, syntaxErrors := 0, 0, 0, 0
	for i, chunks := range streams {
		decoder := newOpenAIStreamDecoder(&sseChunkReader{chunks: cloneChunks(chunks)}, nil, nil)
		var got []any
		for decoder.Next() {
			event := decoder.Event()
			got = append(got, canonical([]byte(event.Data)))
		}
		want := expected[i]
		var wantItems []any
		for _, raw := range want.Items {
			wantItems = append(wantItems, canonical(raw))
		}
		items += len(wantItems)
		problem := ""
		if !reflect.DeepEqual(got, wantItems) && (len(got) != 0 || len(wantItems) != 0) {
			problem = "items differ"
		}
		var decodeErr *openAIStreamDecodeError
		switch {
		case want.Error == nil:
			if decoder.Err() != nil {
				problem = "unexpected error " + decoder.Err().Error()
			}
		case want.Error.Name == "SyntaxError":
			syntaxErrors++
			if !errors.As(decoder.Err(), &decodeErr) || decodeErr.APIError != nil {
				problem = "want a malformed-JSON error, got " + sseErrorText(decoder.Err())
			}
		case want.Error.Name == "APIError":
			apiErrors++
			if decoder.Err() == nil || decoder.Err().Error() != want.Error.Message {
				problem = "want API error " + want.Error.Message + ", got " + sseErrorText(decoder.Err())
			}
		default:
			problem = "oracle error " + want.Error.Name + ": " + want.Error.Message
		}
		if problem != "" {
			differing++
			if differing <= 6 {
				var body []byte
				for _, c := range chunks {
					body = append(body, c...)
				}
				t.Errorf("case %d: %s\n  body %q\n  got  %v\n  want %v (%+v)", i, problem, body, got, wantItems, want.Error)
			}
		}
	}
	t.Logf("%d streams, %d items, %d API errors, %d malformed JSON, %d differing", len(streams), items, apiErrors, syntaxErrors, differing)
	if differing > 0 {
		t.Fatalf("%d streams differ from the SDK", differing)
	}
}

func sseErrorText(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}

// sseChunkReader hands out its chunks one Read at a time.
type sseChunkReader struct {
	chunks [][]byte
}

func (r *sseChunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if len(r.chunks[0]) == 0 {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

func cloneChunks(chunks [][]byte) [][]byte {
	out := make([][]byte, len(chunks))
	for i, chunk := range chunks {
		out[i] = bytes.Clone(chunk)
	}
	return out
}
