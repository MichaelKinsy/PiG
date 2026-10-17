//go:build !pig_strip_mistral_conversations

package ai

// pi: packages/ai/src/api/mistral-conversations.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

// The Mistral request URL expression runs in Node on the service-URL base URLs (schemes, hosts, userinfo, ports, trailing slashes, queries, fragments, dot segments and percent-encoded text) plus
// opaque and path-less bases: Pig's mistralChatCompletionsURL must give Pi's string and fail exactly when a URL constructor throws.
func TestMistralChatCompletionsURLMatchesPiOnSeededProbes(t *testing.T) {
	var bases []string
	for _, probe := range serviceURLProbes() {
		bases = append(bases, probe.BaseURL)
	}
	bases = append(bases, "", "mailto:a@b", "data:text/plain,x", "foo:bar", "foo:/p/q", "foo://h", "foo://h/x?y#z", "file:///a:b", "file:///a/b", "https://api.mistral.ai", "https://api.mistral.ai/", "https://api.mistral.ai/v1", "https://proxy.test/root?token=abc#frag", "https://proxy.test/root///")
	payload, err := json.Marshal(bases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/mistral_url.mjs")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []*string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(bases) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(bases))
	}
	succeeded, differing := 0, 0
	for i, base := range bases {
		text, err := mistralChatCompletionsURL(base)
		if expected[i] != nil {
			succeeded++
		}
		if (err == nil) != (expected[i] != nil) || (err == nil && text != *expected[i]) {
			if differing++; differing <= 12 {
				want, have := "<throws>", "<error>"
				if expected[i] != nil {
					want = *expected[i]
				}
				if err == nil {
					have = text
				}
				t.Errorf("%q\n  Pig %q\n  Pi  %q", base, have, want)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(bases))
	}
	if succeeded < 1000 || succeeded > len(bases)-500 {
		t.Errorf("probes are one-sided: %d succeed", succeeded)
	}
}
