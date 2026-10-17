//go:build !pig_strip_google_vertex

package ai

// pi: packages/ai/src/compat.ts
// pi: packages/ai/src/api/google-vertex.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type trimSiteProbe struct {
	Op    string `json:"op"`
	Value string `json:"value"`
}

// trimSiteProbes wrap keys, URLs and placeholders in every character JavaScript's trim and Go's TrimSpace disagree on (U+FEFF, U+0085, U+180E, U+200B, U+2028, U+2029, U+00A0, U+3000) and in ones they agree on, so
// each helper sees blank, padded, placeholder-shaped and ordinary values.
func trimSiteProbes() []trimSiteProbe {
	random := rand.New(rand.NewSource(20260950))
	pad := []string{"", "", "", " ", "\t", "\n", "\r", "\ufeff", "\u0085", "\u180e", "\u200b", "\u2028", "\u2029", "\u00a0", "\u3000", "\u000b", "\u000c", "\u2003"}
	cores := []string{"", "sk-abc", "gcp-vertex-credentials", "<authenticated>", "<key>", "<a>b>", "<>", "<\n>", "<a b>", "https://us-central1-aiplatform.googleapis.com/v1", "https://x.example/{location}/v1", "{location}", "https://x.example", "<gcp-vertex-credentials>"}
	var probes []trimSiteProbe
	for range 4500 {
		core := cores[random.Intn(len(cores))]
		value := pad[random.Intn(len(pad))] + pad[random.Intn(len(pad))] + core + pad[random.Intn(len(pad))] + pad[random.Intn(len(pad))]
		probes = append(probes, trimSiteProbe{Op: []string{"hasKey", "customBase", "vertexKey"}[random.Intn(3)], Value: value})
	}
	return probes
}

// hasExplicitApiKey, resolveCustomBaseUrl and resolveApiKey (cut from Pi's compat.ts and google-vertex.ts) run in Node on the same values: each trims with String.prototype.trim, which strips U+FEFF and not U+0085,
// where Go's strings.TrimSpace does the opposite.
func TestJSTrimSitesMatchPi(t *testing.T) {
	probes := trimSiteProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	src := filepath.Join(root, ".upstream", "current", "packages", "ai", "src")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/trim_sites.mjs", filepath.Join(src, "compat.ts"), filepath.Join(src, "api", "google-vertex.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	differing, nonEmpty := 0, 0
	for i, probe := range probes {
		var got any
		switch probe.Op {
		case "hasKey":
			got = hasExplicitAPIKey(probe.Value)
		case "customBase":
			if value := resolveCustomBaseURL(probe.Value); value != "" {
				got = value
			}
		default:
			if value := resolveVertexAPIKey(probe.Value); value != "" {
				got = value
			}
		}
		var want any
		if err := json.Unmarshal(expected[i], &want); err != nil {
			t.Fatal(err)
		}
		if want != nil && want != false {
			nonEmpty++
		}
		if !reflect.DeepEqual(got, want) {
			if differing++; differing <= 12 {
				t.Errorf("%+v\n  Pig %#v\n  Pi  %#v", probe, got, want)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if nonEmpty < 1000 || nonEmpty > len(probes)-300 {
		t.Errorf("probes are one-sided: %d answers are non-empty", nonEmpty)
	}
}
