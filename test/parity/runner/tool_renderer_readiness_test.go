//go:build parity

package runner

import (
	"path/filepath"
	"testing"
)

// Pi 0.87.1 interactive-mode.ts:1114-1118,1214-1259 makes tmux warnings conditional, not a startup acknowledgement. The integration capture on both hosts instead contains this idle editor/footer after loading tool-renderers.mjs.
func TestExpandedToolRendererReadinessUsesIdleUI(t *testing.T) {
	sc, err := LoadScenario(filepath.Join("..", "scenarios", "extensions-runtime", "25-tool-renderers-expanded.toml"))
	if err != nil {
		t.Fatal(err)
	}
	const idle = "[Extensions]\n  tool-renderers.mjs\n\n────────────────────────────────────────────────────────────────────────────────────────────────────\n\n────────────────────────────────────────────────────────────────────────────────────────────────────\n/isolated/project\n0.0%/128k (auto)                                                                              faux-1\n"
	const warning = "tmux extended-keys is off. Modified Enter keys may not work."
	for _, host := range []struct{ name, pattern string }{{"pig", sc.Tmux.ReadyPatternPig}, {"pi", sc.Tmux.ReadyPatternPi}} {
		t.Run(host.name, func(t *testing.T) {
			if !paneMatchesReadyPattern(idle, host.pattern) {
				t.Errorf("idle editor/footer without an optional warning did not satisfy %q", host.pattern)
			}
			if !paneMatchesReadyPattern(idle+warning, host.pattern) {
				t.Error("a legitimate diagnostic prevented idle UI readiness")
			}
			if paneMatchesReadyPattern(warning, host.pattern) {
				t.Error("diagnostic alone counted as UI readiness")
			}
			if paneMatchesReadyPattern("[Extensions]\n  tool-renderers.mjs\n", host.pattern) {
				t.Error("registration banner alone counted as UI readiness")
			}
		})
	}
}
