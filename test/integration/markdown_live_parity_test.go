//go:build integration

package integration

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

var trailingSGRPattern = regexp.MustCompile(`(?:\x1b\[[0-9;]*m)+$`)

func TestParity_MarkdownInteractiveTmuxCapture(t *testing.T) {
	prompt := "Render the markdown parity fixture exactly."
	startNeedle := "Fixture Heading"
	endNeedle := "example.com/docs"

	type capture struct {
		plain    string
		escaped  string
		block    string
		escBlock string
	}
	results := map[string]capture{}

	for _, cfg := range []systemConfig{deterministicGopiConfig(t), deterministicUpstreamConfig(t)} {
		cfg := cfg
		t.Run(cfg.name, func(t *testing.T) {
			var markers []string
			if cfg.name == "pig" {
				markers = []string{interactiveReadyMarker}
			} else {
				markers = []string{"Press ctrl+o to show full startup help"}
			}
			session, cleanup := launchSystemWithReady(t, cfg, 25*time.Second, markers...)
			defer cleanup()
			time.Sleep(800 * time.Millisecond)

			if err := tmuxCommand("send-keys", "-t", session, "-l", prompt).Run(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond)
			if err := tmuxCommand("send-keys", "-t", session, "Enter").Run(); err != nil {
				t.Fatal(err)
			}
			pane, ok := waitForPaneContains(session, 10*time.Second, endNeedle)
			if !ok {
				t.Fatalf("%s: interactive markdown response did not appear:\n%s", cfg.name, pane)
			}
			settleAfter()

			plain := capturePane(t, session)
			escaped := capturePaneEscaped(t, session)
			block := slicePaneByNeedles(t, plain, startNeedle, endNeedle)
			escBlock := slicePaneByNeedles(t, escaped, startNeedle, endNeedle)

			results[cfg.name] = capture{plain: plain, escaped: escaped, block: block, escBlock: escBlock}

			writeParityArtifact(t, "19-markdown-interactive-tmux-capture", cfg.name, "frame-01-pane.txt", plain)
			writeParityArtifact(t, "19-markdown-interactive-tmux-capture", cfg.name, "frame-01-pane-escaped.txt", escaped)
			writeParityArtifact(t, "19-markdown-interactive-tmux-capture", cfg.name, "frame-02-markdown-block.txt", block)
			writeParityArtifact(t, "19-markdown-interactive-tmux-capture", cfg.name, "frame-02-markdown-block-escaped.txt", escBlock)

			quietExit(t, session)
		})
	}

	up := results["upstream"]
	gp := results["pig"]
	if gp.block != up.block {
		t.Errorf("interactive markdown visible capture mismatch\n--- pig ---\n%s\n--- upstream ---\n%s", gp.block, up.block)
	}
	if normalizeMarkdownEscapedTmuxBlock(gp.escBlock) != normalizeMarkdownEscapedTmuxBlock(up.escBlock) {
		t.Errorf("interactive markdown escaped capture mismatch\n--- pig ---\n%s\n--- upstream ---\n%s", gp.escBlock, up.escBlock)
	}
}

func normalizeMarkdownEscapedTmuxBlock(s string) string {
	lines := strings.Split(normalizeEscapedTmuxBlock(s), "\n")
	for i, line := range lines {
		lines[i] = trailingSGRPattern.ReplaceAllString(line, "")
	}
	return strings.Join(lines, "\n")
}
