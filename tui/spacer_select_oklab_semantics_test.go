package tui

import (
	"math"
	"os"
	"runtime"
	"testing"
)

// packages/tui/src/oklab.ts:70-73 oklabToOkhslLightness: 0.5 * (K3*x - K1 + sqrt((K3*x - K1)^2 + 4*K2*K3*x)) with K1 = 0.206, K2 = 0.03, K3 = (1 + K1) / (1 + K2).
// The expected values are node's results for that expression.
func TestOklabToOkhslLightnessMatchesPi(t *testing.T) {
	for _, tc := range []struct{ x, want float64 }{
		{0, 0},
		{0.1, 0.029631373306766642},
		{0.25, 0.14661412944746693},
		{0.5, 0.42114056260896976},
		{0.75, 0.7092972572794943},
		{1, 1},
	} {
		if got := OklabToOkhslLightness(tc.x); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("OklabToOkhslLightness(%v) = %v, want %v", tc.x, got, tc.want)
		}
	}
}

// components/spacer.ts setLines and select-list.ts setSelectedIndex change what the component renders; Pig's invalidatable marks the component dirty so the
// surface that draws it redraws it (D91, tui.go invalidatable). Upstream's own invalidate() is a no-op, so only the Go mark is asserted here.
func TestSpacerSetLinesAndSelectListSetSelectedIndexMarkTheComponentDirty(t *testing.T) {
	spacer := NewSpacer(1)
	if _, ok := Component(spacer).(*Spacer); !ok {
		t.Fatalf("NewSpacer returned %T, want a *Spacer component", spacer)
	}
	spacer.NeedsRedraw()
	spacer.SetLines(3)
	if !spacer.IsDirty() || len(spacer.Render(10)) != 3 {
		t.Errorf("SetLines left the spacer dirty=%v with %d lines, want dirty with 3", spacer.IsDirty(), len(spacer.Render(10)))
	}
	list := NewSelectList([]SelectItem{{Value: "a", Label: "a"}, {Value: "b", Label: "b"}}, 5, SelectListTheme{})
	list.NeedsRedraw()
	list.SetSelectedIndex(1)
	if !list.IsDirty() {
		t.Error("SetSelectedIndex did not mark the list dirty")
	}
}

// components/truncated-text.ts render with a width narrower than twice its padding: Pig clamps the horizontal padding to (width-1)/2 and keeps one content
// cell (D66), so no row is wider than the viewport. The rows are (top padding, content, bottom padding) for text "abcdef", paddingX 5 and paddingY 1.
func TestTruncatedTextClampsItsPaddingToTheViewport(t *testing.T) {
	const dot = "\x1b[0m.\x1b[0m"
	if _, ok := Component(NewTruncatedText("x", 0, 0)).(*TruncatedText); !ok {
		t.Fatal("NewTruncatedText is not a *TruncatedText component")
	}
	for _, tc := range []struct {
		width int
		want  []string
	}{
		{0, []string{"", dot, ""}},
		{1, []string{" ", dot, " "}},
		{2, []string{"  ", "\x1b[0m..\x1b[0m", "  "}},
		{3, []string{"   ", " " + dot + " ", "   "}},
		{4, []string{"    ", " \x1b[0m..\x1b[0m ", "    "}},
		{5, []string{"     ", "  " + dot + "  ", "     "}},
	} {
		got := NewTruncatedText("abcdef", 5, 1).Render(tc.width)
		if len(got) != len(tc.want) {
			t.Errorf("width %d: %q, want %q", tc.width, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("width %d row %d = %q, want %q", tc.width, i, got[i], tc.want[i])
			}
		}
	}
}

// terminal.ts isAppleTerminalSession reads the platform and TERM_PROGRAM of the running process: the exported function answers what the platform test answers for them.
func TestIsAppleTerminalSessionReadsThePlatformAndTermProgram(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	if got, want := IsAppleTerminalSession(), runtime.GOOS == "darwin"; got != want {
		t.Errorf("IsAppleTerminalSession() = %v on %s with TERM_PROGRAM=Apple_Terminal, want %v", got, runtime.GOOS, want)
	}
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if IsAppleTerminalSession() {
		t.Error("IsAppleTerminalSession() is true for TERM_PROGRAM=iTerm.app")
	}
	if got, want := os.Getenv("TERM_PROGRAM"), "iTerm.app"; got != want {
		t.Fatalf("TERM_PROGRAM = %q, want %q", got, want)
	}
}
