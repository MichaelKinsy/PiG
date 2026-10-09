package rpcclient

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/modes/rpc/jsonl.ts

// attachJsonlLineReader and serializeJsonLine (jsonl.ts): LF-only framing (U+2028/2029 and a lone CR stay inside a record), one trailing CR removed,
// blank records delivered, a final unterminated record delivered at end, multi-byte characters split across chunk boundaries, and the
// JSON.stringify form of each value. The pinned Pi and PiG split the same chunked byte streams into the same records.
func TestJSONLReaderMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	pieces := []string{"{\"a\":1}", "x", "é", "😀", "\u2028", "\u2029", "\r", "\n", "\r\n", "\n\n", " ", "\u00a0", "\ufeff", "\"q\\n\"", "\x00", "\u0085"}
	values := []string{`{"a":1}`, `"\u2028\u2029"`, `"<&>"`, `-0`, `[1e21,1e-7,0.1]`, `null`, `{"é":"😀"}`, `1.0`, `"\u007f\u0001"`}
	type testCase struct {
		Chunks []string `json:"chunks"`
		Values []string `json:"values"`
	}
	var cases []testCase
	var streams [][]byte
	for range 2500 {
		var text strings.Builder
		for range rng.Intn(12) {
			text.WriteString(pieces[rng.Intn(len(pieces))])
		}
		data := []byte(text.String())
		var chunks []string
		for len(data) > 0 {
			n := 1 + rng.Intn(len(data))
			if rng.Intn(2) == 0 {
				n = min(len(data), 1+rng.Intn(3))
			}
			chunks = append(chunks, hex.EncodeToString(data[:n]))
			data = data[n:]
		}
		if chunks == nil {
			chunks = []string{}
		}
		streams = append(streams, []byte(text.String()))
		cases = append(cases, testCase{Chunks: chunks, Values: values})
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/jsonl_reader.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Lines      []string `json:"lines"`
		Serialized []string `json:"serialized"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	var serialized []string
	for _, value := range values {
		var decoded any
		if err := json.Unmarshal([]byte(value), &decoded); err != nil {
			t.Fatal(err)
		}
		line, err := SerializeJsonLine(decoded)
		if err != nil {
			t.Fatal(err)
		}
		serialized = append(serialized, string(line))
	}
	for i, stream := range streams {
		lines := []string{}
		// The stream is delivered in the same chunks: a reader over the whole bytes sees the same records, as the framing is chunk independent.
		if err := ReadJSONLLines(bytes.NewReader(stream), func(line []byte) bool { lines = append(lines, string(line)); return true }); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(lines, want[i].Lines) {
			t.Errorf("stream %q: PiG %q, Pi %q", stream, lines, want[i].Lines)
		}
	}
	// -0 and the lone surrogate differ only by the writer's decode of the value; compare the rest by value, all by bytes where decoding is exact.
	for k, value := range values {
		if serialized[k] != want[0].Serialized[k] {
			t.Errorf("SerializeJsonLine(%s) = %q, Pi %q", value, serialized[k], want[0].Serialized[k])
		}
	}
}
