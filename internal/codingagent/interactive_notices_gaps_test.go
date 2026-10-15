package codingagent

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func renderedNotice(m *InteractiveMode) []string {
	var out []string
	for _, line := range m.chatContainer.Render(60) {
		out = append(out, strings.TrimRight(stripANSITest(line), " "))
	}
	return out
}

// interactive-mode.ts showError / showWarning / showPackageUpdateNotification: each appends a spacer then its notice to the chat — the error at the output pad, the warning at padding one with Pi's "Warning: " prefix, and the package notice between two warning borders listing every package — and an interactive mode without a chat container shows nothing.
// Pi: packages/coding-agent/src/modes/interactive/interactive-mode.ts showError (:4492), showWarning (:4499), showPackageUpdateNotification (:4627).
func TestInteractiveNoticesAppendSpacedPaddedBlocksToTheChat(t *testing.T) {
	restoreStartupTheme(t)
	newMode := func(pad int) *InteractiveMode {
		return &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 60, 20), outputPad: pad}
	}

	m := newMode(3)
	m.showError("boom")
	if got, want := renderedNotice(m), []string{"", "   Error: boom"}; !slices.Equal(got, want) {
		t.Errorf("showError = %q, want %q", got, want)
	}
	m.showError("again")
	if got := renderedNotice(m); len(got) != 4 || got[2] != "" || got[3] != "   Error: again" {
		t.Errorf("a second error = %q, want it appended after a spacer", got)
	}
	(&InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 60, 20)}).showError("ignored") // no chat container: must not panic

	w := newMode(3)
	w.showWarning("careful")
	if got, want := renderedNotice(w), []string{"", " Warning: careful"}; !slices.Equal(got, want) {
		t.Errorf("showWarning = %q, want %q (padding 1 regardless of the output pad)", got, want)
	}

	p := newMode(0)
	p.ShowPackageUpdateNotification([]string{"npm:left-pad", "git:github.com/u/r"})
	got := renderedNotice(p)
	if len(got) < 8 || got[0] != "" {
		t.Fatalf("package notice = %q", got)
	}
	idx := func(sub string) int {
		return slices.IndexFunc(got, func(l string) bool { return strings.Contains(l, sub) })
	}
	title, run, list, first, second := idx("Package Updates Available"), idx("update --extensions"), idx("Packages:"), idx("- npm:left-pad"), idx("- git:github.com/u/r")
	if title <= 1 || run <= title || list <= run || first <= list || second <= first {
		t.Errorf("package notice order wrong: %q", got)
	}
	if !strings.Contains(got[1], "─") || !strings.Contains(got[len(got)-1], "─") {
		t.Errorf("package notice is not between two borders: %q", got)
	}
	empty := newMode(0)
	empty.ShowPackageUpdateNotification(nil)
	if text := strings.Join(renderedNotice(empty), "\n"); strings.Contains(text, "- ") || !strings.Contains(text, "Packages:") {
		t.Errorf("a notice with no packages = %q", text)
	}
}
