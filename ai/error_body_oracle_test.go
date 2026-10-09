package ai

// pi: packages/ai/src/utils/error-body.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type errorBodyProbe struct {
	Op      string  `json:"op"`
	JSON    string  `json:"json"`
	Text    string  `json:"text"`
	Max     int     `json:"max"`
	Status  *int    `json:"status"`
	Body    *string `json:"body"`
	Message string  `json:"message"`
	Carries bool    `json:"carries"`
	Prefix  *string `json:"prefix"`
}

// errorBodyProbes feed the three string helpers values whose JavaScript and Go renderings can differ: numbers across the 1e21 and 1e-7 exponent
// thresholds, -0, strings with U+2028/U+2029, U+007F, quotes and slashes, HTML characters, non-BMP text, nested containers, and texts around the
// UTF-16 truncation limit whose cut falls inside a surrogate pair.
func errorBodyProbes() []errorBodyProbe {
	random := rand.New(rand.NewSource(20260936))
	numbers := []string{"0", "-0", "1", "1.5", "0.1", "1e21", "1e20", "123456789012345680000", "1e-7", "1e-6", "0.000001", "1.7976931348623157e308", "5e-324", "100", "1E3", "-1.25e+2", "9007199254740993"}
	strs := []string{`""`, `"a"`, `"<b>&amp;"`, `"\u2028\u2029"`, "\"\u2028\"", `"\u007f"`, `"\u0000"`, `"a\"b\\c/d"`, `"é😀"`, `"\ud83d\ude00"`, `"\ud83d"`, `"\u001f"`, `"\b\f\n\r\t"`, `"\u00e9"`}
	var gen func(depth int) string
	gen = func(depth int) string {
		switch r := random.Intn(8); {
		case r < 3 || depth == 0:
			return numbers[random.Intn(len(numbers))]
		case r < 5:
			return strs[random.Intn(len(strs))]
		case r == 5:
			return []string{"true", "false", "null"}[random.Intn(3)]
		case r == 6:
			parts := make([]string, random.Intn(4))
			for i := range parts {
				parts[i] = gen(depth - 1)
			}
			return "[" + strings.Join(parts, ",") + "]"
		default:
			keys := []string{"a", "b", "error", "message", "z", "é", "<k>", "k\u2028"}
			random.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
			var parts []string
			for _, key := range keys[:random.Intn(4)] {
				raw, _ := json.Marshal(key)
				parts = append(parts, string(raw)+":"+gen(depth-1))
			}
			sortedJSONKeys(parts)
			return "{" + strings.Join(parts, ",") + "}"
		}
	}
	var probes []errorBodyProbe
	for range 700 {
		probes = append(probes, errorBodyProbe{Op: "stringify", JSON: gen(3)})
	}
	for range 400 {
		text := strings.Repeat("a", random.Intn(8)) + []string{"😀", "é", "日", "\U0001F9D1\u200d\U0001F9B0", ""}[random.Intn(5)] + strings.Repeat("b", random.Intn(8))
		probes = append(probes, errorBodyProbe{Op: "truncate", Text: text, Max: random.Intn(14)})
	}
	statuses := []*int{nil, new(0), new(400), new(429), new(-1)}
	bodies := []*string{nil, new(""), new("body text"), new("{\"error\":1}"), new("😀")}
	prefixes := []*string{nil, new(""), new("OpenAI"), new("x (y)")}
	for range 400 {
		probes = append(probes, errorBodyProbe{Op: "format", Status: statuses[random.Intn(5)], Body: bodies[random.Intn(5)], Message: []string{"", "msg", "msg body text"}[random.Intn(3)], Carries: random.Intn(2) == 0, Prefix: prefixes[random.Intn(4)]})
	}
	return probes
}

// sortedJSONKeys orders object members by key text so Go's sorted-key encoding and JavaScript's insertion order agree; integer-like keys are left out of the
// key list because JavaScript lists them first, in numeric order.
func sortedJSONKeys(parts []string) {
	for i := 1; i < len(parts); i++ {
		for j := i; j > 0 && parts[j] < parts[j-1]; j-- {
			parts[j], parts[j-1] = parts[j-1], parts[j]
		}
	}
}

// error-body.ts runs from the installed pi-ai in Node against the same probes: safeJsonStringify's text, truncateErrorText's UTF-16 cut and count,
// and formatProviderError's composition must match Pi's.
func TestErrorBodyHelpersMatchPiOnSeededProbes(t *testing.T) {
	probes := errorBodyProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/error_body.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	differing := 0
	for i, probe := range probes {
		var got string
		switch probe.Op {
		case "stringify":
			// Provider bodies reach Go as raw JSON text (a decoded Go string cannot hold a lone surrogate), and as decoded values for a few adapters.
			got = SafeJsonStringify(json.RawMessage(probe.JSON))
			var value any
			if !strings.Contains(probe.JSON, `\ud83d"`) && json.Unmarshal([]byte(probe.JSON), &value) == nil {
				if decoded := SafeJsonStringify(value); decoded != got && !strings.Contains(probe.JSON, "{") {
					t.Errorf("decoded %s: %q, raw %q", probe.JSON, decoded, got)
				}
			}
		case "truncate":
			got = TruncateErrorText(probe.Text, probe.Max)
		default:
			norm := NormalizedProviderError{Status: probe.Status, Body: probe.Body, Message: probe.Message, MessageCarriesBody: probe.Carries}
			if probe.Prefix != nil {
				got = FormatProviderError(norm, *probe.Prefix)
			} else {
				got = FormatProviderError(norm)
			}
		}
		if got != expected[i] {
			if differing++; differing <= 10 {
				t.Errorf("%s %+v differs from Pi:\n  Pig %q\n  Pi  %q", probe.Op, probe, got, expected[i])
			}
		}
	}
	if differing > 10 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
}
