package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Upstream main.ts reportDiagnostics wraps each diagnostic in chalk.red, chalk.yellow or chalk.dim. chalk keeps an empty string empty, re-opens its color after
// a close code already in the text, and closes and re-opens it around every line break (keeping a carriage return before the break). PiG wrapped the whole
// text once, so a multi-line diagnostic carried its color across line breaks, an embedded reset ended it early and an empty info line printed bare codes.
func TestFormatReportedDiagnosticMatchesChalk(t *testing.T) {
	type diagnostic struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	var diagnostics []diagnostic
	for _, kind := range []string{"error", "warning", "info"} {
		for _, message := range []string{
			"", "plain", "two\nlines", "crlf\r\nline", "trailing\n", "\nleading", "a\n\nb", "lone\rcr",
			"inner \x1b[39mreset\x1b[39m twice", "inner \x1b[22mdim reset", "both \x1b[31mred\x1b[39m\nand line", "\x1b[0m full reset", "日本\n😀",
		} {
			diagnostics = append(diagnostics, diagnostic{kind, message})
		}
	}
	input, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/report_diagnostics.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, d := range diagnostics {
		if got := formatReportedDiagnostic(AgentSessionRuntimeDiagnostic(d), true); got != want[i] {
			t.Errorf("%s %q = %q, want %q", d.Type, d.Message, got, want[i])
		}
	}
}
