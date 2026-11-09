package codingagent

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The render contracts compare whole session selector frames with pinned Pi byte for byte, so every theme, glyph, layout and numeric choice of
// SessionList.render and SessionSelectorHeader.render is observed.

func sessionWantWithinWidth(width int) func(t *testing.T, tr sessionContractTrace) {
	width = max(width, 1)
	return func(t *testing.T, tr sessionContractTrace) {
		t.Helper()
		for f, frame := range tr.Frames {
			for _, line := range frame {
				if got := widthx.VisibleWidth(line); got > width {
					t.Fatalf("frame %d: %q is %d cells wide in a %d-cell terminal", f, line, got, width)
				}
			}
		}
	}
}

type sessionRenderScript struct {
	name string
	keys []string
}

var sessionRenderScripts = []sessionRenderScript{
	{"start", nil},
	{"sort-recent", []string{skCtrlS}},
	{"sort-fuzzy", []string{skCtrlS, skCtrlS}},
	{"named", []string{skCtrlN}},
	{"named-all-scope", []string{skTab, skCtrlN}},
	{"path", []string{skCtrlP}},
	{"all-scope", []string{skTab}},
	{"all-scope-path", []string{skTab, skCtrlP}},
	{"delete-confirm", []string{skCtrlD}},
	{"delete-confirm-moved", []string{skDown, skCtrlD}},
	{"delete-current", []string{skCtrlD, skEscape, skCtrlS, skPageDn, skCtrlD}},
	{"page-down", []string{skPageDn}},
	{"page-down-twice", []string{skPageDn, skPageDn}},
	{"page-down-to-end", []string{skPageDn, skPageDn, skPageDn, skPageDn}},
	{"down-3", []string{skDown, skDown, skDown}},
	{"search-hit", []string{"t", "h"}},
	{"search-miss", []string{"q", "q", "q"}},
	{"search-miss-named", []string{skCtrlN, "q"}},
	{"search-regex", []string{"r", "e", ":", "[a-c]"}},
	{"rename-panel", []string{skCtrlR}},
	{"delete-after-rename-cancel", []string{skCtrlR, skEscape, skCtrlD}},
}

// TestSessionListRenderMatchesPi compares the search row, empty messages, row layout (cursor, tree prefix, name or first message, colours, age, cwd and path
// parts, selection background) and the position line with Pi over every fixture, script, width and both themes.
func TestSessionListRenderMatchesPi(t *testing.T) {
	restoreStartupTheme(t)
	var cases []sessionContractCase
	for _, fixture := range []string{"small", "big", "unnamed", "one", "empty", "empty-current", "ages", "text", "threads"} {
		for _, width := range []int{1, 3, 7, 12, 20, 24, 40, 72, 100} {
			for _, script := range sessionRenderScripts {
				cases = append(cases, sessionContractCase{name: fmt.Sprintf("%s/w%03d/%s", fixture, width, script.name), fixture: fixture, width: width, keys: script.keys, currentPath: sessionS3, check: sessionWantWithinWidth(width)})
			}
		}
		for _, script := range sessionRenderScripts {
			cases = append(cases, sessionContractCase{name: fmt.Sprintf("%s/light/%s", fixture, script.name), fixture: fixture, theme: "light", keys: script.keys, currentPath: sessionS3, check: sessionWantWithinWidth(100)})
		}
	}
	// The current session's accent colour and the delete-pending error colour on the same row.
	for _, fixture := range []string{"big", "threads"} {
		path := map[string]string{"big": sessionS3, "threads": "/work/threads/c.jsonl"}[fixture]
		for _, script := range sessionRenderScripts {
			cases = append(cases, sessionContractCase{name: fmt.Sprintf("%s/current/%s", fixture, script.name), fixture: fixture, keys: script.keys, currentPath: path})
		}
	}
	// Every window position of the 14-row list: the window follows the highlight and is clamped at both ends, with the position line only while rows are clipped.
	for pos := range 14 {
		keys := []string{skCtrlS}
		for range pos {
			keys = append(keys, skDown)
		}
		cases = append(cases, sessionContractCase{name: fmt.Sprintf("window/row%02d", pos), fixture: "big", keys: keys})
	}
	// A list that fits the window draws no position line.
	cases = append(cases, sessionContractCase{name: "window/fits-without-a-position-line", fixture: "small", keys: []string{skDown}, check: func(t *testing.T, tr sessionContractTrace) {
		t.Helper()
		if got := sessionScroll(tr.last()); got != "" {
			t.Errorf("position line %q drawn for a list that fits", got)
		}
	}})
	runSessionContracts(t, cases)
}

// TestSessionHeaderRenderMatchesPi runs SessionSelectorHeader.render over every width, scope, name filter, sort, path state, confirmation and status
// message, with default and rebound hint keys and with and without the rename hint.
func TestSessionHeaderRenderMatchesPi(t *testing.T) {
	restoreStartupTheme(t)
	states := []sessionRenderScript{
		{"default", nil},
		{"all-scope", []string{skTab}},
		{"recent", []string{skCtrlS}},
		{"fuzzy", []string{skCtrlS, skCtrlS}},
		{"named", []string{skCtrlN}},
		{"path-on", []string{skCtrlP}},
		{"all-named-fuzzy-path", []string{skTab, skCtrlN, skCtrlS, skCtrlS, skCtrlP}},
		{"delete-confirm", []string{skCtrlD}},
		{"cannot-delete-current", []string{skCtrlS, skDown, skDown, skDown, skCtrlD}},
		{"deleted", []string{skCtrlD, skEnter}},
	}
	bindings := map[string]map[string][]string{
		"default": nil,
		"rebound": {"tui.input.tab": {"ctrl+g"}, "app.session.toggleSort": {"ctrl+q"}, "app.session.toggleNamedFilter": {"ctrl+w"}, "app.session.delete": {"ctrl+e", "ctrl+f"}, "app.session.togglePath": {"alt+p"}, "app.session.rename": {"f2"}, "tui.select.confirm": {"ctrl+y"}, "tui.select.cancel": {"ctrl+z"}},
	}
	var cases []sessionContractCase
	for _, state := range states {
		for bname, b := range bindings {
			for width := 0; width <= 140; width++ {
				cases = append(cases, sessionContractCase{name: fmt.Sprintf("%s/%s/w%03d", state.name, bname, width), fixture: "big", currentPath: sessionS3, width: width, zeroWidth: width == 0, bindings: b, keys: state.keys, check: sessionWantWithinWidth(width)})
			}
			cases = append(cases, sessionContractCase{name: fmt.Sprintf("%s/%s/light", state.name, bname), fixture: "big", currentPath: sessionS3, theme: "light", bindings: b, keys: state.keys})
		}
		for _, width := range []int{20, 40, 72, 100} {
			cases = append(cases, sessionContractCase{name: fmt.Sprintf("%s/no-rename/w%03d", state.name, width), fixture: "big", currentPath: sessionS3, noRename: true, width: width, keys: state.keys, check: sessionWantWithinWidth(width)})
		}
	}
	// The loading header: no progress, progress, a finished load, and each scope.
	for _, progress := range [][]sessionProgress{nil, {{Loaded: 3, Total: 14}}, {{Loaded: 14, Total: 14, Partial: 14}}} {
		for width := 1; width <= 110; width++ {
			cases = append(cases, sessionContractCase{name: fmt.Sprintf("loading-%d/w%03d", len(progress), width), fixture: "big", width: width, pending: &sessionPending{Progress: progress, ReleaseBefore: -1}, check: sessionWantWithinWidth(width)})
		}
	}
	runSessionContracts(t, cases)
}
