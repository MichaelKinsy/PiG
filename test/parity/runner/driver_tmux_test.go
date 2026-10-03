//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCoalesceTmuxKeysBatchesOnlyAdjacentLiteralInput(t *testing.T) {
	keys := []string{"Space", "t", "h", "e", "m", "e", "Enter", "hex:1b", "x", "y"}
	want := []string{"Space", "theme", "Enter", "hex:1b", "xy"}
	if got := coalesceTmuxKeys(keys); !slices.Equal(got, want) {
		t.Fatalf("coalesced keys = %#v, want %#v", got, want)
	}
}

func TestPaneMatchesReadyPattern_UpstreamNoModelFooterAlsoCountsAsReady(t *testing.T) {
	pane := "~/repo\n0.0%/0                                                                                       unknown\n"
	if !paneMatchesReadyPattern(pane, "$0.000") {
		t.Fatalf("expected no-model upstream footer to satisfy $0.000 ready pattern")
	}
}

func TestPaneMatchesReadyPattern_UpstreamModelFooterAlsoCountsAsReady(t *testing.T) {
	pane := "~/repo\n0.0%/128k                                                                         (test-faux) faux-1\n"
	if !paneMatchesReadyPattern(pane, "$0.000") {
		t.Fatalf("expected model upstream footer to satisfy $0.000 ready pattern")
	}
}

func TestPaneMatchesReadyPattern_UpstreamModelFooterWithGitCountsAsReady(t *testing.T) {
	pane := "~/repo\n↑10 ↓8 0.0%/128k                                                                  (test-faux) faux-1\n"
	if !paneMatchesReadyPattern(pane, "$0.000") {
		t.Fatalf("expected git-prefixed model footer to satisfy $0.000 ready pattern")
	}
	if !paneMatchesReadyPattern(pane, "(auto)") {
		t.Fatalf("expected git-prefixed model footer to satisfy auto ready pattern")
	}
}

func TestPaneMatchesReadyPattern_UpstreamNonZeroFooterCountsAsReady(t *testing.T) {
	pane := "~/repo\n↑600 ↓350 0.4%/128k                                                               (test-faux) faux-1\n"
	if !paneMatchesReadyPattern(pane, "$0.000") {
		t.Fatalf("expected non-zero upstream footer to satisfy $0.000 ready pattern")
	}
	if !paneMatchesReadyPattern(pane, "(auto)") {
		t.Fatalf("expected non-zero upstream footer to satisfy auto ready pattern")
	}
}

func TestPaneMatchesReadyPattern_UpstreamUnknownPercentFooterCountsAsReady(t *testing.T) {
	pane := "~/repo\n↑10 ↓8 ?/128k                                                                     (test-faux) faux-1\n"
	if !paneMatchesReadyPattern(pane, "$0.000") {
		t.Fatalf("expected unknown-percent upstream footer to satisfy $0.000 ready pattern")
	}
	if !paneMatchesReadyPattern(pane, "(auto)") {
		t.Fatalf("expected unknown-percent upstream footer to satisfy auto ready pattern")
	}
}

func TestPaneMatchesReadyPattern_ExactPatternStillWorks(t *testing.T) {
	pane := "$0.000 (sub) 0.0%/264k (auto)                                                    gpt-5-mini • medium\n"
	if !paneMatchesReadyPattern(pane, "$0.000") {
		t.Fatalf("expected exact ready pattern match")
	}
}

func TestPaneMatchesReadyPattern_Auto_UpstreamFooterCountsAsReady(t *testing.T) {
	pane := "~/repo\n0.0%/128k                                                                         (test-faux) faux-1\n"
	if !paneMatchesReadyPattern(pane, "(auto)") {
		t.Fatalf("expected auto ready pattern to match upstream footer")
	}
}

func TestPaneMatchesReadyPattern_Auto_BarePromptCountsAsReady(t *testing.T) {
	pane := "\n\n>\n\n"
	if !paneMatchesReadyPattern(pane, "(auto)") {
		t.Fatalf("expected auto ready pattern to match visible prompt row")
	}
}

// The pinned interactive-mode.ts header has no Ready paragraph. These scenario patterns must recognize the idle FooterComponent.render output observed in tmux.
func TestScenarioReadinessMatchesPinnedPiFooter(t *testing.T) {
	for _, tc := range []struct {
		path string
		pane string
	}{
		{"selectors/07-login-dialog.toml", "0.0%/0       unknown\n"},
		{"extensions-runtime/05-session-lifecycle.toml", "0.0%/128k (auto)       faux-1\n"},
		{"model-resolver-selector/15-model-picker-google-and-hint.toml", "0.0%/200k (auto)       model-two • thinking off\n"},
		{"model-resolver-selector/16-model-picker-enter-session-only.toml", "0.0%/200k (auto)       model-two • thinking off\n"},
		{"model-resolver-selector/17-model-picker-save-default.toml", "0.0%/200k (auto)       model-two • thinking off\n"},
		{"model-resolver-selector/18-model-picker-save-default-scope-order.toml", "0.0%/128k (auto)       model-one\n"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			sc, err := LoadScenario(filepath.Join("..", "scenarios", tc.path))
			if err != nil {
				t.Fatal(err)
			}
			for label, pattern := range map[string]string{"pig": sc.Tmux.ReadyPatternPig, "pi": sc.Tmux.ReadyPatternPi} {
				if !paneMatchesReadyPattern(tc.pane, pattern) {
					t.Errorf("%s ready pattern %q rejects its idle footer %q", label, pattern, tc.pane)
				}
				if paneMatchesReadyPattern("", pattern) {
					t.Errorf("empty pane satisfies %s readiness", label)
				}
			}
		})
	}
}

func TestPaneMatchesCaptureSurface_RequiresConfiguredMarkers(t *testing.T) {
	cfg := TmuxDriverConfig{CaptureStart: "Type to search:", CaptureEnd: "(1/1)"}
	pane := "Type to search:\n...\n(1/1)"
	if !paneMatchesCaptureSurface(pane, cfg) {
		t.Fatalf("expected pane with both capture markers to count as reached surface")
	}
	if paneMatchesCaptureSurface(pane, TmuxDriverConfig{}) {
		t.Fatalf("expected empty capture config not to count as reached surface")
	}
}

func TestCropCapture_EndRegexMatchesVariableSuffix(t *testing.T) {
	pane := "$ expr 20 + 22\n\n42\n\nTook 0.1s\n\n\n42\n────"
	got := cropCapture(pane, "$ expr 20 + 22", "", `Took \d+\.\ds`, false)
	want := "$ expr 20 + 22\n\n42\n\nTook 0.1s"
	if got != want {
		t.Fatalf("end_regex crop mismatch\ngot:  %q\nwant: %q", got, want)
	}
}

func TestCropCapture_EndRegexAlsoMatchesZeroVariant(t *testing.T) {
	pane := "$ expr 20 + 22\n\n42\n\nTook 0.0s\n\n\n42\n────"
	got := cropCapture(pane, "$ expr 20 + 22", "", `Took \d+\.\ds`, false)
	want := "$ expr 20 + 22\n\n42\n\nTook 0.0s"
	if got != want {
		t.Fatalf("end_regex crop mismatch\ngot:  %q\nwant: %q", got, want)
	}
}

func TestPaneMatchesCaptureSurface_EndRegex(t *testing.T) {
	cfg := TmuxDriverConfig{CaptureStart: "$ expr", CaptureEndRegex: `Took \d+\.\ds`}
	if !paneMatchesCaptureSurface("$ expr 20 + 22\nTook 0.5s\n", cfg) {
		t.Fatalf("expected pane with regex-matched end to count as reached surface")
	}
	if paneMatchesCaptureSurface("$ expr 20 + 22\nstill running\n", cfg) {
		t.Fatalf("expected pane without regex-matched end not to count as reached surface")
	}
}

func TestPaneSatisfiesWaitRequiresNewEvidenceAfterKeys(t *testing.T) {
	patterns := []string{"Reloaded", "status"}
	baseline := "Reloaded status"
	if paneSatisfiesWait("Reloaded status", patterns, baseline, true) {
		t.Fatal("stale pre-action markers satisfied wait")
	}
	if paneSatisfiesWait("Reloaded status\nunrelated animation", patterns, baseline, true) {
		t.Fatal("unrelated pane mutation satisfied stale markers")
	}
	if paneSatisfiesWait("Reloaded Reloaded status", patterns, baseline, true) {
		t.Fatal("retained state marker satisfied a new-output barrier")
	}
	if !paneSatisfiesWait("Reloaded status\nReloaded status", patterns, baseline, true) {
		t.Fatal("new copies of every marker did not satisfy barrier")
	}
}

// A repaint under load writes cursor addressing such as ESC[42;1H into the terminal transcript. The wait counts the transcript with the pane, so a control sequence that carries the digits of an expected answer must not satisfy it (session/66 and every wait on "42" passed before the answer arrived, and /clone then ran ahead of the first turn).
func TestWaitIgnoresDigitsInTerminalControlSequences(t *testing.T) {
	transcriptPath := filepath.Join(t.TempDir(), "terminal-transcript.log")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(transcriptPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	history := func() string {
		return capturePaneHistory(t.Context(), "parity-no-such-session", transcriptPath)
	}
	patterns := []string{"42"}
	write("ready\x1b[?25l")
	baseline := history()

	write("ready\x1b[?25l\x1b[42;1H\x1b[2K\x1b[38;5;42m\x1b]8;;http://host/42\x07 What is 20+22?\x1b]8;;\x1b\\")
	if paneSatisfiesWait(history(), patterns, baseline, true) {
		t.Fatal("digits inside control sequences satisfied the wait")
	}

	write("ready\x1b[?25l\x1b[42;1H 42")
	if !paneSatisfiesWait(history(), patterns, baseline, true) {
		t.Fatal("printed 42 did not satisfy the wait")
	}
}

// The transcript is read while tmux pipe-pane is still appending to it, and Pi 1.0.0 fullscreen positions each row with ESC[row;1H and no line break (packages/tui/src/tui-alt-screen.ts:1732-1742). A wait must not see the parameters of a sequence cut off at the end of the file, the payload of a kitty graphics APC (terminal-image.ts:240), or text from two rows joined into one.
func TestTranscriptTextKeepsOnlyDisplayedText(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"plain", "What is 20+22?\r\n42", "What is 20+22?\r\n42"},
		{"style inside a line", "4\x1b[1m2\x1b[0m", "42"},
		{"erase keeps the cursor", "\r\x1b[2K42\x1b[K", "\r42"},
		{"csi cut off at the end", "ready\x1b[42", "ready"},
		{"csi parameters cut off at the end", "ready\x1b[42;", "ready"},
		{"lone escape at the end", "ready\x1b", "ready"},
		{"osc cut off at the end", "ready\x1b]8;;http://host/42", "ready"},
		{"osc hyperlink", "\x1b]8;;http://host/42\x07link\x1b]8;;\x1b\\", "link"},
		{"osc aborted by another escape", "\x1b]8;;http://host/42\x1b[0mtext", "text"},
		{"kitty graphics apc", "\x1b_Ga=T,f=100;QUJDNDJE\x1b\\done", "done"},
		{"cursor marker apc", "ab\x1b_pi:c\x07cd", "abcd"},
		{"alt-screen rows", "\x1b[1;1H\x1b[2Kcost 4\x1b[2;1H\x1b[2K2 tokens", "\ncost 4\n2 tokens"},
		{"relative moves", "4\x1b[1A2\x1b[3B4\x1b[2C2", "4\n2\n4\n2"},
		{"restore cursor", "4\x1b72\x1b84", "42\n4"},
		{"charset designation", "\x1b(B42", "42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := transcriptText([]byte(tc.raw)); got != tc.want {
				t.Fatalf("transcriptText(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParityRunIDIsSafeForTmuxSessionName(t *testing.T) {
	t.Setenv("PIG_PARITY_RUN_ID", "job / 42")
	if got := parityRunID(); got != "job-42" {
		t.Fatalf("run ID = %q, want job-42", got)
	}
}

func TestReadProcessExitStatusDistinguishesRunningAndExited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status")
	if code, exited, err := readProcessExitStatus(path); err != nil || exited || code != 0 {
		t.Fatalf("missing status = (%d, %t, %v), want running", code, exited, err)
	}
	if err := os.WriteFile(path, []byte("17\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, exited, err := readProcessExitStatus(path); err != nil || !exited || code != 17 {
		t.Fatalf("written status = (%d, %t, %v), want exited 17", code, exited, err)
	}
}
