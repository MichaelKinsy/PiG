package tui

import (
	"maps"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// upstreamAltScreenBindings are pi-tui v0.87.1's default keys for the
// alt-screen actions, installed as user bindings so key-driven tests exercise
// upstream's keys.
var upstreamAltScreenBindings = map[string][]string{
	KBAltScreenHalfPageUp:     {},
	KBAltScreenHalfPageDown:   {},
	KBAltScreenLineUp:         {},
	KBAltScreenLineDown:       {},
	KBAltScreenPreviousPrompt: {"ctrl+shift+up", "ctrl+up"},
	KBAltScreenNextPrompt:     {"ctrl+shift+down", "ctrl+down"},
	KBAltScreenSearch:         {"ctrl+shift+f"},
	KBAltScreenSearchNext:     {"enter", "ctrl+g"},
	KBAltScreenSearchPrevious: {"shift+enter", "ctrl+shift+g"},
	KBAltScreenSearchClose:    {"escape"},
}

// useAltScreenBindings installs upstream's alt-screen keys plus overrides as
// the global TUI keybindings for one test.
func useAltScreenBindings(t *testing.T, overrides map[string][]string) {
	t.Helper()
	previous := globalTUIKeybindings
	bindings := map[string][]string{}
	maps.Copy(bindings, upstreamAltScreenBindings)
	maps.Copy(bindings, overrides)
	SetKeybindings(NewTUIKeybindingsManager(bindings))
	t.Cleanup(func() { globalTUIKeybindings = previous })
}

// TestAltScreenSearchNormalizesAcrossRows ports upstream "searches normalized
// rendered transcript text across rows".
func TestAltScreenSearchNormalizesAcrossRows(t *testing.T) {
	got := FindAltScreenSearchMatches([]string{"alpha QUICK", "brown fox"}, "quick brown")
	want := []AltScreenSearchMatch{{Segments: []AltScreenSearchSegment{{Row: 0, StartCol: 6, EndCol: 11}, {Row: 1, StartCol: 0, EndCol: 5}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %+v, want %+v", got, want)
	}
}

// TestAltScreenSearchMapsMatchesToRenderedColumns ports upstream "maps
// normalized ASCII and Unicode search matches back to rendered columns".
func TestAltScreenSearchMapsMatchesToRenderedColumns(t *testing.T) {
	got := FindAltScreenSearchMatches([]string{"\x1b[31mfoo  bar\x1b[0m", "A界🙂éZ"}, "oo   bar\nA界🙂é")
	want := []AltScreenSearchMatch{{Segments: []AltScreenSearchSegment{
		{Row: 0, StartCol: 1, EndCol: 3},
		{Row: 0, StartCol: 5, EndCol: 8},
		{Row: 1, StartCol: 0, EndCol: 6},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %+v, want %+v", got, want)
	}
}

// TestAltScreenSearchIndexReusesMatches ports upstream "reuses indexed
// transcript matches until the query or rendered lines change".
func TestAltScreenSearchIndexReusesMatches(t *testing.T) {
	var index AltScreenSearchIndex
	initial, changed := index.Search([]string{"alpha needle", "omega"}, "needle")
	if !changed || len(initial) != 1 {
		t.Fatalf("initial = %+v changed=%v", initial, changed)
	}
	cached, changed := index.Search([]string{"alpha needle", "omega"}, "needle")
	if changed || &cached[0] != &initial[0] {
		t.Fatal("unchanged lines and query must reuse the cached matches")
	}
	changedQuery, changed := index.Search([]string{"alpha needle", "omega"}, "omega")
	if !changed || &changedQuery[0] == &initial[0] {
		t.Fatal("a new query must recompute matches")
	}
	if want := []AltScreenSearchSegment{{Row: 1, StartCol: 0, EndCol: 5}}; !reflect.DeepEqual(changedQuery[0].Segments, want) {
		t.Fatalf("segments = %+v, want %+v", changedQuery[0].Segments, want)
	}
	changedLines, changed := index.Search([]string{"alpha needle", "no match"}, "omega")
	if !changed || len(changedLines) != 0 {
		t.Fatalf("changed lines = %+v changed=%v, want no matches", changedLines, changed)
	}
}

// TestAltScreenSearchComponentRendersControls ports upstream "renders
// transcript search with a muted placeholder and right-aligned controls".
func TestAltScreenSearchComponentRendersControls(t *testing.T) {
	useAltScreenBindings(t, nil)
	component := NewAltScreenSearchComponent(func(string) {}, nil)
	rendered := component.Render(48)
	lines := visibleFrameLines(rendered)
	if len(lines) != 3 {
		t.Fatalf("rendered %d lines, want 3", len(lines))
	}
	for _, line := range lines {
		if widthx.VisibleWidth(line) != 48 {
			t.Fatalf("line %q is not 48 cells wide", line)
		}
	}
	for i, pattern := range []string{`^┌─+┐$`, `^│ Find in transcript +│$`, `^└─+ ↑ Shift\+Enter · ↓ Enter ─┘$`} {
		if !regexp.MustCompile(pattern).MatchString(lines[i]) {
			t.Fatalf("line %d = %q, want %s", i, lines[i], pattern)
		}
	}
	if !strings.Contains(rendered[1], "\x1b[2m") {
		t.Fatal("the placeholder must be muted")
	}
	controls := []rune(lines[2])
	column := func(needle string, offset int, last bool) int {
		text := string(controls)
		index := strings.Index(text, needle)
		if last {
			index = strings.LastIndex(text, needle)
		}
		return len([]rune(text[:index])) + offset
	}
	for _, check := range []struct {
		column int
		want   int
	}{
		{column("↑", 0, false), -1},
		{column("Shift+Enter", 5, false), -1},
		{column("·", 0, false), 0},
		{column("↓", 0, false), 1},
		{column("Enter", 2, true), 1},
	} {
		if got := component.GetNavigationDirectionAt(2, check.column); got != check.want {
			t.Fatalf("direction at column %d = %d, want %d", check.column, got, check.want)
		}
	}

	component.HandleInput("n")
	component.SetResult(0, 2)
	populatedRender := component.Render(48)
	populated := visibleFrameLines(populatedRender)
	if !strings.Contains(populated[1], "n") || !strings.Contains(populated[1], "1/2") {
		t.Fatalf("populated row = %q", populated[1])
	}
	if !strings.Contains(populatedRender[1], "\x1b[2m 1/2 \x1b[22m") {
		t.Fatalf("the result counter must be muted: %q", populatedRender[1])
	}
	for _, line := range populated {
		if strings.Contains(line, "Find in transcript") {
			t.Fatal("the placeholder must hide once a query is typed")
		}
	}
}

// TestAltScreenSearchComponentFormatsKeys pins upstream's key labels: each
// part capitalized, alt named Option on macOS, and "Unbound" without keys.
func TestAltScreenSearchComponentFormatsKeys(t *testing.T) {
	previous := altScreenPlatform
	t.Cleanup(func() { altScreenPlatform = previous })
	altScreenPlatform = "darwin"
	if got := formatSearchKey([]string{"alt+enter"}); got != "Option+Enter" {
		t.Fatalf("darwin alt label = %q", got)
	}
	altScreenPlatform = "linux"
	if got := formatSearchKey([]string{"alt+enter"}); got != "Alt+Enter" {
		t.Fatalf("linux alt label = %q", got)
	}
	if got := formatSearchKey(nil); got != "Unbound" {
		t.Fatalf("unbound label = %q", got)
	}
}

// TestAltScreenSearchComponentCompactsNarrowControls pins upstream's
// fallback to bare arrows when the labelled controls do not fit, and its
// three-glyph rendering at width one.
func TestAltScreenSearchComponentCompactsNarrowControls(t *testing.T) {
	useAltScreenBindings(t, nil)
	component := NewAltScreenSearchComponent(func(string) {}, nil)
	lines := visibleFrameLines(component.Render(12))
	if !strings.HasSuffix(lines[2], " ↑ ↓ ─┘") {
		t.Fatalf("narrow controls = %q", lines[2])
	}
	if got := component.Render(1); !reflect.DeepEqual(got, []string{"┌", "│", "└"}) {
		t.Fatalf("width-one render = %q", got)
	}
}
