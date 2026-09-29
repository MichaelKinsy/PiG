//go:build parity

package runner

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateOutcome_NotContainAssertions(t *testing.T) {
	cases := []struct {
		name      string
		asserts   AssertSpec
		pigOut    string
		piOut     string
		wantFail  bool
		wantMatch string
	}{
		{
			name:     "both_not_contain_passes",
			asserts:  AssertSpec{BothNotContain: []string{"panic:"}},
			pigOut:   "all good",
			piOut:    "all good",
			wantFail: false,
		},
		{
			name:      "both_not_contain_fails_on_pig",
			asserts:   AssertSpec{BothNotContain: []string{"panic:"}},
			pigOut:    "panic: boom",
			piOut:     "all good",
			wantFail:  true,
			wantMatch: "pig output unexpectedly contains \"panic:\"",
		},
		{
			name:      "pi_not_contain_fails",
			asserts:   AssertSpec{PiNotContain: []string{"goroutine "}},
			pigOut:    "all good",
			piOut:     "goroutine 1",
			wantFail:  true,
			wantMatch: "pi output unexpectedly contains \"goroutine \" (asymmetric)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &ScenarioOutcome{
				Scenario: &Scenario{Name: tc.name, Assert: tc.asserts},
				Pig:      SystemResults{System: "pig", Runs: []Result{{Output: tc.pigOut}}},
				Pi:       SystemResults{System: "pi", Runs: []Result{{Output: tc.piOut}}},
			}
			EvaluateOutcome(o)
			if got := len(o.Failures) > 0; got != tc.wantFail {
				t.Fatalf("failures=%v, wantFail=%v (%v)", o.Failures, tc.wantFail, o.Failures)
			}
			if tc.wantMatch != "" {
				matched := false
				for _, f := range o.Failures {
					if f == tc.wantMatch {
						matched = true
						break
					}
				}
				if !matched {
					t.Fatalf("failures=%v, want %q", o.Failures, tc.wantMatch)
				}
			}
		})
	}
}

func TestEvaluateOutcome_EscapedOutputEqual(t *testing.T) {
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: "escaped", Assert: AssertSpec{EscapedOutputEqual: true}},
		Pig:      SystemResults{System: "pig", Runs: []Result{{Escaped: "\x1b[31mhello\x1b[0m"}}},
		Pi:       SystemResults{System: "pi", Runs: []Result{{Escaped: "\x1b[31mhello\x1b[0m"}}},
	}
	EvaluateOutcome(o)
	if len(o.Failures) != 0 {
		t.Fatalf("unexpected failures: %v", o.Failures)
	}

	o = &ScenarioOutcome{
		Scenario: &Scenario{Name: "escaped-mismatch", Assert: AssertSpec{EscapedOutputEqual: true}},
		Pig:      SystemResults{System: "pig", Runs: []Result{{Escaped: "\x1b[31mhello\x1b[0m"}}},
		Pi:       SystemResults{System: "pi", Runs: []Result{{Escaped: "\x1b[32mhello\x1b[0m"}}},
	}
	EvaluateOutcome(o)
	if len(o.Failures) == 0 {
		t.Fatal("wanted escaped output mismatch failure")
	}
}

func TestEvaluateOutcomeEqualityChecksEveryDurabilityPair(t *testing.T) {
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: "second-run-drift", Assert: AssertSpec{OutputEqual: true}},
		Pig: SystemResults{Runs: []Result{
			{Output: "same"},
			{Output: "pig drift"},
		}},
		Pi: SystemResults{Runs: []Result{
			{Output: "same"},
			{Output: "pi stable"},
		}},
	}
	EvaluateOutcome(o)
	if len(o.Failures) != 1 || o.Failures[0] != `run 2 output mismatch: pig="pig drift" pi="pi stable"` {
		t.Fatalf("failures = %#v", o.Failures)
	}
}

func TestFullscreenDoesNotErasePathBearingRows(t *testing.T) {
	files := []string{
		"03-main-screen-history-redraw.toml",
		"04-main-screen-assistant-streaming.toml",
		"05-main-screen-tool-streaming.toml",
		"06-main-screen-tool-settled.toml",
		"07-main-screen-editor-growth.toml",
		"08-main-screen-editor-shrink.toml",
		"09-main-screen-compaction-in-progress.toml",
		"10-main-screen-compaction-settled.toml",
	}
	cwdPaths := []string{
		"/tmp/parity-snap-cwd-123",
		"/private/var/tmp/parity-snap-cwd-456",
		`C:\Users\runner\AppData\Local\Temp\parity-snap-cwd-789`,
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			scenario, err := LoadScenario(filepath.Join("..", "scenarios", "fullscreen", file))
			if err != nil {
				t.Fatal(err)
			}
			for _, cwd := range cwdPaths {
				o := &ScenarioOutcome{
					Scenario: &Scenario{Name: file, Assert: AssertSpec{
						OutputNormalizedEqual: true,
						NormalizeReplace:      scenario.Assert.NormalizeReplace,
					}},
					Pig: SystemResults{System: "pig", Runs: []Result{{Output: cwd}}},
					Pi:  SystemResults{System: "pi", Runs: []Result{{Output: "/tmp/parity-snap-cwd-reference"}}},
				}
				EvaluateOutcome(o)
				if len(o.Failures) == 0 {
					t.Fatalf("path-bearing row %q was erased", cwd)
				}
			}
		})
	}
}

// The complete transcript row remains observable even when old captures raced collapse. Fix synchronization rather than removing it.
func TestEvaluateOutcome_CompactionInProgressRetainsLiveToolLine(t *testing.T) {
	scenario, err := LoadScenario(filepath.Join("..", "scenarios", "fullscreen", "09-main-screen-compaction-in-progress.toml"))
	if err != nil {
		t.Fatal(err)
	}
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: "09-main-screen-compaction-in-progress", Assert: AssertSpec{
			OutputNormalizedEqual: true,
			NormalizeReplace:      scenario.Assert.NormalizeReplace,
		}},
		Pig: SystemResults{System: "pig", Runs: []Result{{Output: " LIVE-TOOL-24\n\n Took 1.5s\n\n\n LIVE-TOOL-DONE\n\n── ⠋ Compacting context... (escape to cancel) ──────────────────────────────────\n"}}},
		Pi:  SystemResults{System: "pi", Runs: []Result{{Output: "\n Took 1.5s\n\n\n LIVE-TOOL-DONE\n\n── ⠙ Compacting context... (escape to cancel) ──────────────────────────────────\n"}}},
	}
	EvaluateOutcome(o)
	if len(o.Failures) == 0 {
		t.Fatal("extra Pig transcript row was hidden")
	}

	// The opposite-direction evidence must remain visible too.
	o2 := &ScenarioOutcome{
		Scenario: o.Scenario,
		Pig:      SystemResults{System: "pig", Runs: []Result{{Output: "\n Took 2.1s\n\n\n LIVE-TOOL-DONE\n\n── ⠸ Compacting context... (escape to cancel) ──────────────────────────────────\n"}}},
		Pi:       SystemResults{System: "pi", Runs: []Result{{Output: " LIVE-TOOL-24\n\n Took 2.1s\n\n\n LIVE-TOOL-DONE\n\n── ⠦ Compacting context... (escape to cancel) ──────────────────────────────────\n"}}},
	}
	EvaluateOutcome(o2)
	if len(o2.Failures) == 0 {
		t.Fatal("missing Pig transcript row was hidden")
	}
}

func TestEvaluateOutcomeNormalizedComparatorCannotWeakenOutputEqual(t *testing.T) {
	o := &ScenarioOutcome{
		Scenario: &Scenario{Assert: AssertSpec{OutputEqual: true, OutputNormalizedEqual: true}},
		Pig:      SystemResults{Runs: []Result{{Output: "same   "}}},
		Pi:       SystemResults{Runs: []Result{{Output: "same"}}},
	}
	EvaluateOutcome(o)
	if len(o.Failures) != 1 || !strings.Contains(o.Failures[0], "output mismatch") {
		t.Fatalf("failures = %#v", o.Failures)
	}
}

func TestEvaluateOutcome_NormalizeReplace(t *testing.T) {
	t.Run("masks_timing_difference", func(t *testing.T) {
		o := &ScenarioOutcome{
			Scenario: &Scenario{Name: "norm-replace", Assert: AssertSpec{
				OutputNormalizedEqual: true,
				NormalizeReplace: []NormalizeReplaceRule{
					{Pattern: `Took \d+\.\d+s`, With: "Took Ns", Reason: "Tool duration varies with scheduling."},
				},
			}},
			Pig: SystemResults{System: "pig", Runs: []Result{{Output: "$ expr 20 + 22\n\n42\n\nTook 0.0s"}}},
			Pi:  SystemResults{System: "pi", Runs: []Result{{Output: "$ expr 20 + 22\n\n42\n\nTook 0.1s"}}},
		}
		EvaluateOutcome(o)
		if len(o.Failures) != 0 {
			t.Fatalf("expected no failures after normalize_replace, got %v", o.Failures)
		}
	})

	t.Run("still_catches_real_difference", func(t *testing.T) {
		o := &ScenarioOutcome{
			Scenario: &Scenario{Name: "norm-replace-strict", Assert: AssertSpec{
				OutputNormalizedEqual: true,
				NormalizeReplace: []NormalizeReplaceRule{
					{Pattern: `Took \d+\.\d+s`, With: "Took Ns", Reason: "Tool duration varies with scheduling."},
				},
			}},
			Pig: SystemResults{System: "pig", Runs: []Result{{Output: "$ expr 20 + 22\n\n42\n\nTook 0.0s"}}},
			Pi:  SystemResults{System: "pi", Runs: []Result{{Output: "$ expr 20 + 22\n\n43\n\nTook 0.1s"}}},
		}
		EvaluateOutcome(o)
		if len(o.Failures) == 0 {
			t.Fatal("expected failure on real content difference even after replace")
		}
	})

	t.Run("invalid_regex_fails", func(t *testing.T) {
		o := &ScenarioOutcome{
			Scenario: &Scenario{Name: "norm-replace-bad", Assert: AssertSpec{
				OutputNormalizedEqual: true,
				NormalizeReplace: []NormalizeReplaceRule{
					{Pattern: `[unclosed`, With: "x", Reason: "Invalid pattern must be diagnosed."},
				},
			}},
			Pig: SystemResults{System: "pig", Runs: []Result{{Output: "a"}}},
			Pi:  SystemResults{System: "pi", Runs: []Result{{Output: "a"}}},
		}
		EvaluateOutcome(o)
		matched := false
		for _, f := range o.Failures {
			if contains(f, "invalid normalize_replace pattern") {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("expected invalid-pattern failure, got %v", o.Failures)
		}
	})
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestEvaluateOutcome_ExtensionAssertions(t *testing.T) {
	o := &ScenarioOutcome{
		Scenario: &Scenario{
			Name: "extension-report",
			Assert: AssertSpec{Extensions: ExtensionAssertSpec{
				RegisteredTools: []string{"hello"},
				NoDiagnostics:   true,
			}},
		},
		Pig: SystemResults{Runs: []Result{{Output: `{"valid":true,"name":"example","tools":["hello"],"registered":true}`}}},
		Pi:  SystemResults{Runs: []Result{{Output: `{"valid":true,"skipped":"pig-only"}`}}},
	}
	EvaluateOutcome(o)
	if !o.Passed() {
		t.Fatalf("extension assertions failed unexpectedly: %v", o.Failures)
	}
}

func TestEvaluateOutcome_ExtensionAssertionsFailOnMissingTool(t *testing.T) {
	o := &ScenarioOutcome{
		Scenario: &Scenario{
			Name: "extension-report",
			Assert: AssertSpec{Extensions: ExtensionAssertSpec{
				RegisteredTools: []string{"missing"},
			}},
		},
		Pig: SystemResults{Runs: []Result{{Output: `{"valid":true,"name":"example","tools":["hello"],"registered":true}`}}},
		Pi:  SystemResults{Runs: []Result{{Output: `{"valid":true,"skipped":"pig-only"}`}}},
	}
	EvaluateOutcome(o)
	if o.Passed() {
		t.Fatalf("extension assertions passed, want missing tool failure")
	}
}

func TestEvaluateOutcome_ExtensionAssertionsCheckEveryRun(t *testing.T) {
	o := &ScenarioOutcome{
		Scenario: &Scenario{Assert: AssertSpec{Extensions: ExtensionAssertSpec{RegisteredTools: []string{"hello"}}}},
		Pig: SystemResults{Runs: []Result{
			{Output: `{"valid":true,"tools":["hello"]}`},
			{Output: `{"valid":true,"tools":[]}`},
		}},
	}
	EvaluateOutcome(o)
	if len(o.Failures) != 1 || !strings.Contains(o.Failures[0], "extension run 2 tool missing") {
		t.Fatalf("failures = %#v", o.Failures)
	}
}

// TestEvaluateOutcome_EscapedNormalizeReplace proves escaped_output_equal
// applies scenario normalize_replace rules (so non-deterministic content like
// "Took 0.1s" is masked in the SGR-preserving capture, matching the other
// comparators) while still catching a real byte difference.
func TestEvaluateOutcome_EscapedNormalizeReplace(t *testing.T) {
	const pig = "\x1b[38;2;128;128;128mTook 0.1s\x1b[39m"
	const piTiming = "\x1b[38;2;128;128;128mTook 0.0s\x1b[39m"
	const piColor = "\x1b[38;2;99;99;99mTook 0.1s\x1b[39m"
	rule := []NormalizeReplaceRule{{Pattern: `Took \d+\.\ds`, With: "Took Ns", Reason: "Tool duration varies with scheduling."}}

	cases := []struct {
		name     string
		piEsc    string
		rules    []NormalizeReplaceRule
		wantFail bool
	}{
		{"timing_masked_by_rule", piTiming, rule, false},
		{"timing_unmasked_without_rule", piTiming, nil, true},
		{"real_sgr_diff_still_caught", piColor, rule, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &ScenarioOutcome{
				Scenario: &Scenario{Name: tc.name, Assert: AssertSpec{
					EscapedOutputEqual: true,
					NormalizeReplace:   tc.rules,
				}},
				Pig: SystemResults{System: "pig", Runs: []Result{{Escaped: pig}}},
				Pi:  SystemResults{System: "pi", Runs: []Result{{Escaped: tc.piEsc}}},
			}
			EvaluateOutcome(o)
			if got := len(o.Failures) > 0; got != tc.wantFail {
				t.Fatalf("failures=%v, wantFail=%v", o.Failures, tc.wantFail)
			}
		})
	}
}

// Compaction token totals are measured output, not wall-clock identities.
func TestEvaluateOutcome_CompactionSettledRetainsTokenCounts(t *testing.T) {
	scenario, err := LoadScenario(filepath.Join("..", "scenarios", "fullscreen", "10-main-screen-compaction-settled.toml"))
	if err != nil {
		t.Fatal(err)
	}
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: scenario.Name, Assert: AssertSpec{
			OutputNormalizedEqual: true,
			NormalizeReplace:      scenario.Assert.NormalizeReplace,
		}},
		Pig: SystemResults{System: "pig", Runs: []Result{{Output: "[compaction]\n\n Compacted from 1,890 tokens (ctrl+o to expand)\n"}}},
		Pi:  SystemResults{System: "pi", Runs: []Result{{Output: "[compaction]\n\n Compacted from 1,971 tokens (ctrl+o to expand)\n"}}},
	}
	EvaluateOutcome(o)
	if len(o.Failures) == 0 {
		t.Fatal("compaction token accounting was hidden")
	}
}
