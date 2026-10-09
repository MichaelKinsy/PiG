package ai

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// piStreamingResults runs the installed pi-ai parseStreamingJson over texts and returns its result for each, as a tool-argument object: an
// object result as is, anything else as the empty object.
func piStreamingResults(t *testing.T, texts []string) []any {
	t.Helper()
	payload, err := json.Marshal(texts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/streaming_json.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var answers []streamingJSONAnswer
	if err := json.Unmarshal(output, &answers); err != nil {
		t.Fatal(err)
	}
	if len(answers) != len(texts) {
		t.Fatalf("Pi answered %d of %d texts", len(answers), len(texts))
	}
	results := make([]any, len(answers))
	for i, answer := range answers {
		if object, ok := answer.Stream.(map[string]any); ok && object["$num"] == nil && object["$undefined"] == nil {
			results[i] = nonFiniteMarkersToNull(object)
		} else {
			results[i] = map[string]any{}
		}
	}
	return results
}

// nonFiniteMarkersToNull turns the {"$num": "Infinity"} the oracle writes for a non-finite number into null, which is what the parser's result holds
// (see nonFiniteToNull).
func nonFiniteMarkersToNull(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if _, marker := typed["$num"]; marker && len(typed) == 1 {
			return nil
		}
		for key, item := range typed {
			typed[key] = nonFiniteMarkersToNull(item)
		}
	case []any:
		for i, item := range typed {
			typed[i] = nonFiniteMarkersToNull(item)
		}
	}
	return value
}

func asPlainJSON(value any) any {
	raw, _ := json.Marshal(value)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

// The resumable parser, fed each oracle input in growing chunks, returns at every prefix what parseStreamingJsonArguments returns, and at every
// prefix cut on a character boundary what Pi's parseStreamingJson returns for that prefix.
func TestStreamingArgumentsParserMatchesPiAtEveryChunk(t *testing.T) {
	inputs := streamingJSONInputs()
	random := rand.New(rand.NewSource(11))
	type probe struct {
		text  string
		input int
	}
	var probes []probe
	var texts []string
	for i, text := range inputs {
		for _, step := range []int{1, 4, 9, 1 << 20} {
			parser := &streamingArgumentsParser{}
			for end := 0; ; {
				end = min(len(text), end+1+random.Intn(step))
				prefix := text[:end]
				got, order := parser.parse(prefix)
				want, wantOrder := parseStreamingJsonArguments(prefix)
				if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(order, wantOrder) {
					t.Fatalf("input %d, prefix %q: resumable %#v %#v, whole text %#v %#v", i, prefix, got, order, want, wantOrder)
				}
				if utf8.ValidString(prefix) && random.Intn(4) == 0 {
					probes = append(probes, probe{text: prefix, input: i})
					texts = append(texts, prefix)
				}
				if end == len(text) {
					break
				}
			}
		}
	}
	// Pi's answers for the sampled prefixes: the whole-text parser, and so the resumable one, must agree with them.
	expected := piStreamingResults(t, texts)
	differing := 0
	for i, probe := range probes {
		if got := asPlainJSON(parseStreamingJsonObject(probe.text)); !reflect.DeepEqual(got, expected[i]) {
			if differing++; differing <= 8 {
				t.Errorf("input %d, prefix %q: Pig %v, Pi %v", probe.input, probe.text, got, expected[i])
			}
		}
	}
	if differing > 8 {
		t.Errorf("%d of %d prefixes differ from Pi", differing, len(probes))
	}
	if len(probes) < 5000 {
		t.Fatalf("only %d prefixes probed", len(probes))
	}
}
