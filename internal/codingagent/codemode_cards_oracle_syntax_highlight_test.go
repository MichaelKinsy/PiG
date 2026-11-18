//go:build !pig_strip_syntax_highlight

package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// The codemode tool card (codemode/renderer.ts codemodeRenderers: code preview, nested call rows, model-call cost total, output preview,
// full-output path) through tool-execution.ts against pinned Pi, collapsed and expanded, pending and with results; rows are compared byte for byte.
func TestCodemodeCardsMatchPi(t *testing.T) {
	probes, names := codemodeCardProbes(t)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/codemode_cards.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, got := range renderCodemodeCards(t, probes) {
		if strings.Join(got, "\n") != strings.Join(expected[i], "\n") {
			if failures++; failures <= 4 {
				t.Errorf("%s:\n  Pig %q\n  Pi  %q", names[i], got, expected[i])
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
