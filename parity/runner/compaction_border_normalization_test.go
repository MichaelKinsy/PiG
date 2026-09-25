//go:build parity

package runner

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCompactionBorderNormalizationPreservesLayout(t *testing.T) {
	scenario, err := LoadScenario(filepath.Join("..", "scenarios", "fullscreen", "09-main-screen-compaction-in-progress.toml"))
	if err != nil {
		t.Fatal(err)
	}
	const baseline = "LIVE-TOOL-24\n\nLIVE-TOOL-DONE\n\n── ⠋ Compacting context... (escape to cancel) ─────\n"
	for _, tc := range []struct {
		name, output string
		wantPass     bool
	}{
		{"animation frame", strings.Replace(baseline, "⠋", "⠙", 1), true},
		{"optional final conversation row", strings.Replace(baseline, "LIVE-TOOL-24\n", "", 1), true},
		{"missing tool completion row", strings.Replace(baseline, "LIVE-TOOL-DONE\n", "", 1), false},
		{"extra internal blank row", strings.Replace(baseline, "LIVE-TOOL-DONE\n", "LIVE-TOOL-DONE\n\n", 1), false},
		{"border width", strings.Replace(baseline, "─────", "────", 1), false},
		{"border spacing", strings.Replace(baseline, "── ⠋", "──  ⠋", 1), false},
		{"changed label", strings.Replace(baseline, "Compacting context", "Working", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome := &ScenarioOutcome{
				Scenario: &Scenario{Name: scenario.Name, Assert: AssertSpec{OutputNormalizedEqual: true, NormalizeReplace: scenario.Assert.NormalizeReplace}},
				Pig:      SystemResults{System: "pig", Runs: []Result{{Output: tc.output}}},
				Pi:       SystemResults{System: "pi", Runs: []Result{{Output: baseline}}},
			}
			EvaluateOutcome(outcome)
			if passed := len(outcome.Failures) == 0; passed != tc.wantPass {
				t.Fatalf("passed=%v want=%v failures=%v", passed, tc.wantPass, outcome.Failures)
			}
		})
	}
}

// TestToolStreamingBorderNormalizationPreservesLayout reproduces the
// f2416f71 failure of 05-main-screen-tool-streaming (diff.txt: pig showed
// "── ⠸ Working ────" and pi showed "── ⠦ Working ────" in the same run,
// proving the spinner frame inside the editor top border is exactly as
// non-deterministic on Pi as on Pig). The scenario's pre-existing
// normalize_replace only matched the standalone "  <spinner> Working..."
// line, not this bordered form (no "...", trailing box-drawing dashes), so
// the comparator was strict on a byte neither implementation controls. Mirror
// TestCompactionBorderNormalizationPreservesLayout's proof shape: only the
// spinner code point may vary; every other layout byte remains asserted.
func TestToolStreamingBorderNormalizationPreservesLayout(t *testing.T) {
	scenario, err := LoadScenario(filepath.Join("..", "scenarios", "fullscreen", "05-main-screen-tool-streaming.toml"))
	if err != nil {
		t.Fatal(err)
	}
	const baseline = "LIVE-TOOL-08\n\nElapsed 1.2s\n\n⠋ Working...\n\n── ⠸ Working ─────\n"
	for _, tc := range []struct {
		name, output string
		wantPass     bool
	}{
		{"animation frame", strings.Replace(baseline, "── ⠸ Working", "── ⠦ Working", 1), true},
		{"missing conversation row", strings.Replace(baseline, "LIVE-TOOL-08\n", "", 1), false},
		{"extra internal blank row", strings.Replace(baseline, "LIVE-TOOL-08\n", "LIVE-TOOL-08\n\n", 1), false},
		{"border width", strings.Replace(baseline, "─────", "────", 1), false},
		{"border spacing", strings.Replace(baseline, "── ⠸", "──  ⠸", 1), false},
		{"changed label", strings.Replace(baseline, "── ⠸ Working", "── ⠸ Compacting context...", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome := &ScenarioOutcome{
				Scenario: &Scenario{Name: scenario.Name, Assert: AssertSpec{OutputNormalizedEqual: true, NormalizeReplace: scenario.Assert.NormalizeReplace}},
				Pig:      SystemResults{System: "pig", Runs: []Result{{Output: tc.output}}},
				Pi:       SystemResults{System: "pi", Runs: []Result{{Output: baseline}}},
			}
			EvaluateOutcome(outcome)
			if passed := len(outcome.Failures) == 0; passed != tc.wantPass {
				t.Fatalf("passed=%v want=%v failures=%v", passed, tc.wantPass, outcome.Failures)
			}
		})
	}
}

// TestMainScreenStreamingAssertsMatchPi087BorderLoader replays the Pi 0.87.1
// and Pig captures recorded at f2416f71 (launch-dry-run-f2416f71,
// clone/parity/artifacts/{04-main-screen-assistant-streaming/20260924T154623Z,
// 05-main-screen-tool-streaming/20260924T154621Z}/{pi,pig}.stdout) through
// each scenario's full assert block. Pi 0.87.1 draws the loader only in the
// editor top border ("── ⠸ Working ───"), so a "Working..." both_contain
// fails on Pi itself; the spinner frame differs between the two captures.
func TestMainScreenStreamingAssertsMatchPi087BorderLoader(t *testing.T) {
	border := func(frame string) string {
		return "── " + frame + " Working " + strings.Repeat("─", 67) + "\n\n" + strings.Repeat("─", 80) + "\n"
	}
	footer := func(cwd, pct string) string {
		return "/private/tmp/pig-dry-1rpa_pz9/parity-snap-cwd-" + cwd + "\n↑600 ↓350 " + pct + "/128k (auto)                                                faux-1"
	}
	stream := " LIVE-STREAM-03\n LIVE-STREAM-04\n LIVE-STREAM-05\n LIVE-STREAM-06\n LIVE-STREAM-07\n LIVE-STREAM-08\n\n"
	tool := " LIVE-TOOL-06\n LIVE-TOOL-07\n LIVE-TOOL-08\n\n Elapsed 0.5s\n\n\n"
	for _, tc := range []struct{ file, pig, pi string }{
		{"04-main-screen-assistant-streaming.toml",
			stream + border("⠸") + footer("441271791", "1.4%"),
			stream + border("⠸") + footer("497856648", "1.5%")},
		{"05-main-screen-tool-streaming.toml",
			tool + border("⠸") + footer("875464991", "1.4%"),
			tool + border("⠦") + footer("2835952636", "1.5%")},
	} {
		t.Run(tc.file, func(t *testing.T) {
			scenario, err := LoadScenario(filepath.Join("..", "scenarios", "fullscreen", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			a := scenario.Assert
			a.RuntimeRatioMax = 0
			outcome := &ScenarioOutcome{
				Scenario: &Scenario{Name: scenario.Name, Assert: a},
				Pig:      SystemResults{System: "pig", Runs: []Result{{Output: tc.pig, ReadyOK: true}}},
				Pi:       SystemResults{System: "pi", Runs: []Result{{Output: tc.pi, ReadyOK: true}}},
			}
			EvaluateOutcome(outcome)
			if len(outcome.Failures) != 0 {
				t.Fatalf("recorded Pi 0.87.1 capture fails the scenario asserts: %v", outcome.Failures)
			}
		})
	}
}
