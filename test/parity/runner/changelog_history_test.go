//go:build parity

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTmuxCompleteHistoryRetainsDocumentAboveViewport(t *testing.T) {
	// More than the original scenario's 300 rows and tmux's default history. The expectation is the complete authored input, not an observed count.
	body := "[1.0.0]\n" + strings.Repeat("document-body\n", 3500) + "[2.0.0]\n" + strings.Repeat("final-body\n", 40) + "DOCUMENT-END\n"
	input := filepath.Join(t.TempDir(), "document")
	if err := os.WriteFile(input, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tmuxServerMu.Lock()
	serverErr := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if serverErr != nil {
		t.Fatal(serverErr)
	}
	readDefault := func() string {
		out, err := exec.CommandContext(t.Context(), "tmux", tmuxArgs("show-option", "-g", "-v", "history-limit")...).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	originalLimit := readDefault()
	defer func() {
		if got := readDefault(); got != originalLimit {
			t.Errorf("history capture changed the shared default: %q -> %q", originalLimit, got)
		}
	}()
	for _, history := range []bool{false, true} {
		t.Run(map[bool]string{false: "viewport", true: "history"}[history], func(t *testing.T) {
			sc := &Scenario{Name: "complete-document", Tmux: TmuxDriverConfig{Width: 80, Height: 8, ReadyPatternPig: "READY", ReadyTimeoutSeconds: 5, CaptureHistory: history, Steps: []TmuxStep{{Keys: []string{"show", "Enter"}, WaitContains: []string{"DOCUMENT-END"}, WaitVisibleContains: []string{"DOCUMENT-END"}, WaitTimeoutSeconds: 5}}}}
			bin := BinaryRef{Label: "pig", Path: "sh", Args: []string{"-c", "printf 'READY\\n'; read -r command; cat " + shellQuote(input) + "; sleep 999"}}
			result := (tmuxDriver{}).Run(t.Context(), t, bin, sc)
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if history {
				if !strings.Contains(result.Output, body) {
					t.Fatalf("complete document missing from capture: %d bytes; first=%q", len(result.Output), trunc(result.Output, 120))
				}
			} else if strings.Contains(result.Output, "[1.0.0]") || strings.Contains(result.Output, "[2.0.0]") {
				t.Fatal("control did not scroll every release header out of the viewport")
			}
		})
	}
}

func TestChangelogHeaderComparatorUsesEachDocument(t *testing.T) {
	outcome := &ScenarioOutcome{
		Scenario: &Scenario{Assert: AssertSpec{ChangelogHeadersComplete: true}},
		Pig:      SystemResults{Runs: []Result{{Output: "[1.0.0]\n[7.0.0]\n[2.0.0]\n", ChangelogSource: "## [2.0.0]\n## [7.0.0]\n## [1.0.0]\n"}}},
		Pi:       SystemResults{Runs: []Result{{Output: "[9.0.0]\n", ChangelogSource: "## [9.0.0]\n"}}},
	}
	EvaluateOutcome(outcome)
	if !outcome.Passed() {
		t.Fatal(outcome.Failures)
	}
	outcome.Failures = nil
	outcome.Pi.Runs[0].ChangelogSource = ""
	EvaluateOutcome(outcome)
	if outcome.Passed() {
		t.Fatal("missing independent document source passed")
	}
}

func TestChangelogHeaderComparatorRequiresCompleteOrderedSource(t *testing.T) {
	// Pi utils/changelog.ts:124-160 parses release headers and skips Unreleased; interactive-mode.ts:6506-6514 reverses all entries, regardless of collapseChangelog.
	const source = "## [Unreleased]\nnot released\n## [2.0.0]\nThe documentation may mention Updated to vX.Y.Z.\n## Notes\nnot a release\n## [1.0.0]\nfirst\n"
	const complete = "What's New\n\n[1.0.0]\nfirst\n\n[2.0.0]\nThe documentation may mention Updated to vX.Y.Z.\n"
	for _, tc := range []struct {
		name, output string
		pass         bool
	}{
		{"complete", complete, true},
		{"viewport tail", "The documentation may mention Updated to vX.Y.Z.\n", false},
		{"last release only", "[2.0.0]\nbody\n", false},
		{"wrong order", "[2.0.0]\n[1.0.0]\n", false},
		{"duplicate release", complete + "[2.0.0]\n", false},
		{"condensed startup", "Updated to v2.0.0. Use /changelog to view full changelog.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome := &ScenarioOutcome{Scenario: &Scenario{Assert: AssertSpec{ChangelogHeadersComplete: true}}, Pig: SystemResults{Runs: []Result{{Output: tc.output, ChangelogSource: source}}}, Pi: SystemResults{Runs: []Result{{Output: complete, ChangelogSource: source}}}}
			EvaluateOutcome(outcome)
			if outcome.Passed() != tc.pass {
				t.Fatalf("passed=%t want=%t: %v", outcome.Passed(), tc.pass, outcome.Failures)
			}
		})
	}
}
