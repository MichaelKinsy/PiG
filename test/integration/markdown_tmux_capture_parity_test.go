//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func capturePaneEscaped(t *testing.T, session string) string {
	t.Helper()
	out, _ := tmuxCommand("capture-pane", "-t", session, "-p", "-e").Output()
	return string(out)
}

func slicePaneByNeedles(t *testing.T, pane, startNeedle, endNeedle string) string {
	t.Helper()
	lines := strings.Split(pane, "\n")
	start := -1
	end := -1
	for i, line := range lines {
		if start == -1 && strings.Contains(line, startNeedle) {
			start = i
		}
		if strings.Contains(line, endNeedle) {
			end = i
		}
	}
	if start == -1 || end == -1 || end < start {
		t.Fatalf("failed to slice pane by %q..%q:\n%s", startNeedle, endNeedle, pane)
	}
	return strings.Join(lines[start:end+1], "\n")
}

func writeParityArtifact(t *testing.T, scenario, system, name, content string) string {
	t.Helper()
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	dir := filepath.Join(repoRoot, "tmp", "parity", scenario, system)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir parity dir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write parity artifact %s: %v", path, err)
	}
	return path
}

func TestParity_MarkdownTmuxCapture(t *testing.T) {
	prompt := "Render the markdown parity fixture exactly."
	startNeedle := "Fixture Heading"
	endNeedle := "Read [docs](https://example.com/docs)"

	type capture struct {
		plain      string
		escaped    string
		plainBlock string
		escBlock   string
	}
	results := map[string]capture{}

	for _, sys := range []deterministicPrintSystem{deterministicPrintGopi(t), deterministicPrintUpstream(t)} {
		sys := sys
		t.Run(sys.name, func(t *testing.T) {
			quotedPrompt := "'" + strings.ReplaceAll(prompt, "'", "'\\''") + "'"
			session, _, cleanup := launchDirect(t, sys.name, sys.bin,
				append(sys.args, "--print", quotedPrompt), sys.env, startNeedle)
			defer cleanup()

			pane, ok := waitForPaneContains(session, 10*time.Second, startNeedle, "quoted line", "alpha")
			if !ok {
				t.Fatalf("%s: markdown print capture did not appear:\n%s", sys.name, pane)
			}
			settleAfter()

			plain := capturePane(t, session)
			escaped := capturePaneEscaped(t, session)
			plainBlock := slicePaneByNeedles(t, plain, startNeedle, endNeedle)
			escBlock := slicePaneByNeedles(t, escaped, startNeedle, endNeedle)

			results[sys.name] = capture{plain: plain, escaped: escaped, plainBlock: plainBlock, escBlock: escBlock}

			writeParityArtifact(t, "17-markdown-tmux-capture", sys.name, "frame-01-pane.txt", plain)
			writeParityArtifact(t, "17-markdown-tmux-capture", sys.name, "frame-01-pane-escaped.txt", escaped)
			writeParityArtifact(t, "17-markdown-tmux-capture", sys.name, "frame-02-markdown-block.txt", plainBlock)
			writeParityArtifact(t, "17-markdown-tmux-capture", sys.name, "frame-02-markdown-block-escaped.txt", escBlock)
		})
	}

	up := results["upstream"]
	gp := results["pig"]
	if gp.plainBlock != up.plainBlock {
		t.Errorf("visible markdown tmux capture mismatch\n--- pig ---\n%s\n--- upstream ---\n%s", gp.plainBlock, up.plainBlock)
	}
	if gp.escBlock != up.escBlock {
		t.Errorf("escaped markdown tmux capture mismatch\n--- pig ---\n%s\n--- upstream ---\n%s", gp.escBlock, up.escBlock)
	}
}
