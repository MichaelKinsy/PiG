//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSlashHelpRoundTrip dispatches Pi's /hotkeys command after startup in both renderers, with and without the optional help header. The faux model makes readiness independent of credentials.
func TestSlashHelpRoundTrip(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		for _, quiet := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/quiet=%t", mode, quiet), func(t *testing.T) {
				h := newHarness(t)
				pigHome := t.TempDir()
				agentDir := filepath.Join(pigHome, "agent")
				if err := os.MkdirAll(agentDir, 0o700); err != nil {
					t.Fatal(err)
				}
				settings := fmt.Sprintf(`{"tuiMode":%q,"quietStartup":%t}`, mode, quiet)
				if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
					t.Fatal(err)
				}
				h.startArgsAt([]string{"--model", "test-faux/faux-1"}, t.TempDir(), "PIG_HOME="+pigHome, "PIG_TEST_FAUX=1")
				pane := h.expectContains(startupReadyWait, interactiveReadyMarker, "faux-1")
				if strings.Contains(pane, "Press ctrl+o to show full startup help") == quiet {
					t.Fatalf("startup help does not respect quietStartup=%t:\n%s", quiet, pane)
				}

				h.send("/hotkeys\r")
				h.expectContains(
					3*time.Second,
					"Ctrl+R            : Rename session",
					"Ctrl+/            : undo",
				)
			})
		}
	}
}

// TestShiftEnterInsertsNewline exercises Kitty's ESC+CR Shift+Enter mapping. Pi's keys.ts treats ESC+CR as Shift+Enter only after Kitty negotiation; without it CustomEditor handles Alt+Enter as a follow-up/submit action. The logical tmux key path is tested separately below.
func TestShiftEnterInsertsNewline(t *testing.T) {
	h := newHarness(t)
	h.startArgs([]string{"--model", "test-faux/faux-1"}, "PIG_TEST_FAUX=1")
	h.expectContains(startupReadyWait, interactiveReadyMarker, "faux-1")

	h.send("\x1b[?1u") // Terminal response to the Kitty protocol query.
	h.send("line one")
	h.send("\x1b\r") // Kitty Shift+Enter mapping.
	h.send("line two")
	time.Sleep(500 * time.Millisecond)

	pane := h.capture()
	// Both segments must be visible AND on different rows. If they
	// landed on the same row joined as "line oneline two" the bug
	// returned.
	if !strings.Contains(pane, "line one") || !strings.Contains(pane, "line two") {
		t.Fatalf("missing one of the lines:\n%s", pane)
	}
	if strings.Contains(pane, "line oneline two") {
		t.Fatalf("Shift+Enter regression \u2014 newline was eaten:\n%s", pane)
	}
	// Both lines must remain inside the editor borders, not in a submitted user message.
	assertMultilineEditor(t, pane)
}

// TestShiftEnterViaKeystroke uses tmux's logical `S-Enter` keystroke
// notation instead of raw byte injection. This exercises the chain
// that actually broke for the user:
//
//	tmux Shift+Enter event → (extended-keys protocol enabled by
//	pig via modifyOtherKeys mode 1) → tmux encodes as CSI-u
//	`\x1b[13;2u` (because user's tmux has
//	`extended-keys-format csi-u`) → pig classifyKey routes to
//	actionNewline.
//
// Without the protocol enable in tui.go::EnterRawMode, tmux falls
// back to legacy mode and S-Enter arrives as plain `\r`, which
// pig treats as submit. That's the user-reported regression.
func TestShiftEnterViaKeystroke(t *testing.T) {
	h := newHarness(t)
	h.startArgs([]string{"--model", "test-faux/faux-1"}, "PIG_TEST_FAUX=1")
	h.expectContains(startupReadyWait, interactiveReadyMarker, "faux-1")

	h.send("line one")
	// Logical keystroke: Shift+Enter. Tmux encodes it according to
	// the protocol the running app has signalled for. When pig
	// has enabled modifyOtherKeys mode 1, tmux 3.2+ with
	// extended-keys on translates this to a CSI-u sequence.
	// Without the protocol enable, tmux strips the modifier and
	// sends bare \r.
	h.sendKey("S-Enter")
	h.send("line two")
	time.Sleep(700 * time.Millisecond)

	pane := h.capture()
	if !strings.Contains(pane, "line one") || !strings.Contains(pane, "line two") {
		t.Fatalf("missing one of the lines:\n%s", pane)
	}
	if strings.Contains(pane, "line oneline two") {
		t.Fatalf("Shift+Enter via S-Enter keystroke regressed: protocol enable missing or wrong:\n%s", pane)
	}
	assertMultilineEditor(t, pane)
}

func assertMultilineEditor(t *testing.T, pane string) {
	t.Helper()
	border := strings.Repeat("─", 130) // newHarness fixes the pane width at 130 columns.
	if !strings.Contains(pane, border+"\nline one\nline two\n"+border) {
		t.Fatalf("Shift+Enter must retain both lines in the editor without submitting:\n%s", pane)
	}
}

func TestEditorNewlineShrinkDoesNotFullRedraw(t *testing.T) {
	h := newHarness(t)
	pigHome := t.TempDir()
	h.startArgs([]string{"--model", "test-faux/faux-1"},
		"PIG_HOME="+pigHome,
		"PIG_TEST_FAUX=1",
		"PI_TUI_DEBUG_REDRAW=1",
	)
	h.expectContains(startupReadyWait, interactiveReadyMarker, "faux-1")
	if output, err := tmuxCommand("resize-window", "-t", h.session, "-y", "12").CombinedOutput(); err != nil {
		t.Fatalf("resize tmux window: %v\n%s", err, output)
	}
	time.Sleep(300 * time.Millisecond)

	h.send("EDITOR-SHRINK-LEFT")
	h.sendKey("S-Enter")
	h.send("EDITOR-SHRINK-RIGHT")
	multiline := h.expectContains(3*time.Second, "EDITOR-SHRINK-LEFT", "EDITOR-SHRINK-RIGHT")
	if strings.Contains(multiline, "EDITOR-SHRINK-LEFTEDITOR-SHRINK-RIGHT") {
		t.Fatalf("Shift+Enter did not create a second editor row:\n%s", multiline)
	}
	time.Sleep(300 * time.Millisecond)

	redrawLog := filepath.Join(pigHome, "agent", "pig-debug.log")
	before, err := os.ReadFile(redrawLog)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	h.sendKey("Home")
	h.sendKey("BSpace")
	joined := h.expectContains(3*time.Second, "EDITOR-SHRINK-LEFTEDITOR-SHRINK-RIGHT")
	time.Sleep(300 * time.Millisecond)
	after, err := os.ReadFile(redrawLog)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("ordinary editor shrink triggered a full redraw:\n--- before ---\n%s\n--- after ---\n%s\n--- pane ---\n%s", before, after, joined)
	}
	if strings.Count(joined, "EDITOR-SHRINK-LEFT") != 1 || strings.Count(joined, "EDITOR-SHRINK-RIGHT") != 1 {
		t.Fatalf("editor shrink left a stale or duplicate row:\n%s", joined)
	}
	t.Logf("multiline pane before shrink:\n%s", multiline)
	t.Logf("single-line pane after shrink:\n%s", joined)
}

func TestFullscreenExitOutputPublicPath(t *testing.T) {
	for _, test := range []struct {
		name           string
		exitOutput     string
		wantTranscript bool
	}{
		{name: "transcript", exitOutput: "transcript", wantTranscript: true},
		{name: "resume-hint", exitOutput: "resume-hint", wantTranscript: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			pigHome := t.TempDir()
			agentDir := filepath.Join(pigHome, "agent")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			settings := `{"tuiMode":"fullscreen","fullscreenExitOutput":"` + test.exitOutput + `"}`
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
			sessionDir := filepath.Join(t.TempDir(), "sessions")
			h.startArgs([]string{"--model", "test-faux/faux-1", "--session-dir", sessionDir},
				"PIG_HOME="+pigHome,
				"PIG_TEST_FAUX=1",
			)
			h.expectContains(startupReadyWait, interactiveReadyMarker, "faux-1")

			marker := "Run: expr 20 + 22"
			h.send(marker + "\r")
			h.expectContains(10*time.Second, marker, "Took", "42")
			time.Sleep(300 * time.Millisecond)
			h.sendKey("C-d")
			pane := h.expectContains(5*time.Second, "To resume this session:", "pig --session")
			if strings.Contains(pane, marker) != test.wantTranscript {
				t.Fatalf("exit output contains transcript marker = %t, want %t:\n%s", strings.Contains(pane, marker), test.wantTranscript, pane)
			}

			time.Sleep(700 * time.Millisecond)
			h.send("echo SHELL-RESTORED\r")
			restored := h.expectContains(3*time.Second, "SHELL-RESTORED")
			if isGopiStatusLine(lastNonEmptyLine(restored)) {
				t.Fatalf("terminal did not return to the shell:\n%s", restored)
			}
		})
	}
}

// TestExitCleanShutdown verifies the documented exit paths return to
// the shell without leaving pig or the tmux session in a wedged state.
//
// Both pig and upstream pi expose /quit; neither has /exit. Both
// systems also accept Ctrl+D as a quick-exit. We test both paths.
//
// The "did pig exit?" check is intentionally NOT a substring match
// against the full pane: tmux capture-pane returns scrollback, so the
// pig banner is still visible after exit. We instead inspect the last
// non-empty line of the pane: it should be a shell prompt, not the
// pig status line. (Kaizen 2026-05-10: the previous heuristic
// `Contains("Ctrl+D to exit") && Contains("Ready...")` flagged a
// successful exit as a failure because both strings remained in
// scrollback.)
func TestExitCleanShutdown(t *testing.T) {
	cases := []struct {
		name   string
		action func(h *harness)
	}{
		{
			name:   "slash-quit",
			action: func(h *harness) { h.send("/quit\r") },
		},
		{
			name:   "ctrl-d",
			action: func(h *harness) { h.sendKey("C-d") },
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.start()
			h.expectContains(startupReadyWait, interactiveReadyMarker)

			tc.action(h)
			time.Sleep(700 * time.Millisecond)
			pane := h.capture()

			// Inspect the last non-empty line. After clean exit it is
			// either the user's shell prompt (`❯`, `$`, `%`, `›`) or
			// blank (if the shell scrolled the pane). After failure
			// it's the pig status line containing the model name and
			// "(sub)" / "(auto)" tokens.
			last := lastNonEmptyLine(pane)
			if isGopiStatusLine(last) {
				t.Fatalf("%s: pig appears still running: last line is pig status:\n  last=%q\n  full pane:\n%s",
					tc.name, last, pane)
			}
			t.Logf("%s: clean exit, last line = %q", tc.name, last)
		})
	}
}

// lastNonEmptyLine returns the trimmed last non-empty line of a tmux
// pane capture, or "" if the pane is entirely blank.
func lastNonEmptyLine(pane string) string {
	lines := strings.Split(pane, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], " \t\r")
		if line != "" {
			return line
		}
	}
	return ""
}

// isGopiStatusLine returns true when `line` looks like the running pig
// status line. The status line carries cost, model, and percentage
// tokens that don't appear in any normal shell prompt.
func isGopiStatusLine(line string) bool {
	// Tokens unique to pig's status footer:
	//   "$0.000 (sub)" or "$X.YYY (sub)"
	//   "(auto)"
	//   GPT model labels rendered to the right
	if strings.Contains(line, "(sub)") && strings.Contains(line, "(auto)") {
		return true
	}
	if strings.Contains(line, "GPT-4o") || strings.Contains(line, "GPT-5") {
		return true
	}
	return false
}
