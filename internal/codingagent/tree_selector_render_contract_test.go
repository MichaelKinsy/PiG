package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The render contracts compare whole component frames with pinned Pi byte for byte, so every theme, glyph, layout and numeric choice of
// TreeList.render, renderHorizontalViewport, SearchLine.render, TreeHelp.render and LabelInput.render is observed. The checks add the
// invariants a frame must hold independent of Pi: no row is wider than the terminal.

func wantWithinWidth(width int) func(t *testing.T, tr treeContractTrace) {
	width = max(width, 1) // a zero-width terminal still draws one-cell borders and wrapped help rows
	return func(t *testing.T, tr treeContractTrace) {
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

// treeRowPanned reports whether the highlighted row was shifted left by the horizontal viewport, which closes the clipped body with SGR 0.
func treeRowPanned(frame []string) (panned, found bool) {
	for _, line := range frame {
		if strings.HasPrefix(treePlain(line), "› ") {
			return strings.HasSuffix(line, "\x1b[0m") || strings.Contains(line, "\x1b[0m\x1b["), true
		}
	}
	return false, false
}

// TestTreeHorizontalViewportMatchesPiAtEveryWidth sweeps the terminal width over a deep tree and moves the highlight along its rows, so the pan
// threshold, the anchor-context clamps and the cut at the right edge are each compared with Pi's renderHorizontalViewport.
func TestTreeHorizontalViewportMatchesPiAtEveryWidth(t *testing.T) {
	var cases []treeContractCase
	for width := 0; width <= 140; width++ {
		for _, initial := range []string{"d0", "f1", "d1", "f4", "d6", "f12", "d11", "d12"} {
			cases = append(cases, treeContractCase{name: fmt.Sprintf("deep/w%03d/%s", width, initial), fixture: "deep", leaf: "d12", initial: initial, width: width, zeroWidth: width == 0, check: wantWithinWidth(width)})
		}
	}
	// A body wider than any viewport scrolls with the cursor; moving up and down keeps the pan in step with the highlighted row.
	for _, width := range []int{12, 20, 33, 47, 64, 100} {
		cases = append(cases, treeContractCase{name: fmt.Sprintf("deep/w%03d/walk", width), fixture: "deep", leaf: "d12", width: width, height: 40,
			keys:  []string{tkUp, tkUp, tkUp, tkUp, tkUp, tkDown, tkPageUp, tkPageDown, tkUp, tkLeft, tkRight},
			check: wantWithinWidth(width)})
	}
	traces := runTreeContracts(t, cases)
	panned, flat := 0, 0
	for i, tr := range traces {
		if cases[i].keys != nil {
			continue
		}
		if p, ok := treeRowPanned(tr.lastFrame()); ok {
			if p {
				panned++
			} else {
				flat++
			}
		}
	}
	if panned == 0 || flat == 0 {
		t.Errorf("the width sweep must reach both a panned and an unpanned highlighted row, got %d panned and %d unpanned", panned, flat)
	}
}

type treeRenderScript struct {
	name string
	keys []string
}

// TestTreeListRenderMatchesPi compares the list rows, markers, labels, label times, selected-row styling, windowing and position line with Pi
// over every fixture, the filter modes, folds, the label-time toggle, windows of several heights and both themes.
func TestTreeListRenderMatchesPi(t *testing.T) {
	scripts := []treeRenderScript{
		{"start", nil},
		{"label-time", []string{"T"}},
		{"down-3", []string{tkDown, tkDown, tkDown}},
		{"up-wrap", []string{tkUp, tkUp}},
		{"page-down", []string{tkPageDown}},
		{"page-down-twice", []string{tkPageDown, tkPageDown}},
		{"page-up-from-end", []string{tkPageUp}},
		{"fold-root", []string{tkCtrlLeft}},
		{"fold-second-root-row", []string{tkDown, tkDown, tkCtrlLeft}},
		{"fold-then-label-time", []string{tkCtrlLeft, "T"}},
		{"segment-jumps", []string{tkCtrlRight, tkCtrlRight, tkCtrlLeft, tkCtrlLeft}},
		{"filter-all", []string{tkCtrlA}},
		{"filter-user", []string{tkCtrlU}},
		{"filter-labeled", []string{tkCtrlL}},
		{"filter-labeled-label-time", []string{tkCtrlL, "T"}},
		{"filter-no-tools", []string{tkCtrlT}},
		{"cycle-all-modes", []string{tkCtrlO, tkCtrlO, tkCtrlO, tkCtrlO, tkCtrlO}},
		{"search-hit", []string{"b", "r"}},
		{"search-miss", []string{"z", "z", "z"}},
		{"search-miss-label-time", []string{"z", "z", "T"}},
		{"label-save", []string{"L", "x", tkEnter}},
	}
	leaves := map[string]string{"fork": "b1u", "mixed": "lab", "rich": "re", "forest": "g1x", "single": "only", "wide": "w2", "long": "l14", "deep": "d12", "linear": "a2", "branched": "a2", "multi": "x2", "empty": ""}
	var cases []treeContractCase
	for _, fixture := range []string{"fork", "mixed", "rich", "forest", "single", "wide", "long", "deep", "linear", "branched", "multi", "empty"} {
		for _, width := range []int{1, 7, 24, 38, 64, 100} {
			for _, height := range []int{4, 12, 14, 40} {
				for _, script := range scripts {
					cases = append(cases, treeContractCase{name: fmt.Sprintf("%s/w%03d/h%02d/%s", fixture, width, height, script.name), fixture: fixture, leaf: leaves[fixture], width: width, height: height, keys: script.keys, check: wantWithinWidth(width)})
				}
			}
		}
		for _, script := range scripts {
			cases = append(cases, treeContractCase{name: fmt.Sprintf("%s/light/%s", fixture, script.name), fixture: fixture, leaf: leaves[fixture], theme: "light", height: 12, keys: script.keys, check: wantWithinWidth(100)})
		}
	}
	// Every window position of a list longer than the window: the window follows the highlight and is clamped at both ends.
	for height := 4; height <= 16; height++ {
		for pos := range 15 {
			cases = append(cases, treeContractCase{name: fmt.Sprintf("window/h%02d/row%02d", height, pos), fixture: "long", leaf: "l14", initial: fmt.Sprintf("l%d", pos), height: height})
		}
	}
	traces := runTreeContracts(t, cases)
	for i, tr := range traces {
		if !strings.HasPrefix(cases[i].name, "window/") {
			continue
		}
		var h, row int
		if _, err := fmt.Sscanf(cases[i].name, "window/h%d/row%d", &h, &row); err != nil {
			t.Fatal(err)
		}
		visible := max(5, h/2)
		start := max(0, min(row-visible/2, 15-visible))
		shown := 0
		for _, line := range tr.lastFrame() {
			if text := treePlain(line); strings.Contains(text, "line ") || strings.Contains(text, "chain start") {
				shown++
			}
		}
		if want := min(visible, 15-start); shown != want {
			t.Errorf("%s: %d rows drawn, want %d starting at row %d", cases[i].name, shown, want, start)
		}
	}
}

// TestTreeSearchLineRenderMatchesPi types queries of every shape at many widths and compares the search row with Pi's SearchLine.render.
func TestTreeSearchLineRenderMatchesPi(t *testing.T) {
	queries := map[string][]string{
		"empty":      nil,
		"one":        {"a"},
		"ascii":      {"b", "r", "a", "n", "c", "h", " ", "o", "n", "e"},
		"long":       {"abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789"},
		"wide":       {"宽字符日本語"},
		"emoji":      {"😀😀😀"},
		"combining":  {"e\u0301e\u0301"},
		"spaces":     {" ", " ", "a"},
		"backspaced": {"a", "b", tkBackspace},
	}
	var cases []treeContractCase
	for name, keys := range queries {
		for width := 0; width <= 70; width++ {
			cases = append(cases, treeContractCase{name: fmt.Sprintf("%s/w%03d", name, width), fixture: "fork", leaf: "b1u", width: width, zeroWidth: width == 0, keys: keys, check: wantWithinWidth(width)})
		}
		cases = append(cases,
			treeContractCase{name: name + "/w100", fixture: "fork", leaf: "b1u", keys: keys},
			treeContractCase{name: name + "/light", fixture: "fork", leaf: "b1u", theme: "light", keys: keys},
		)
	}
	runTreeContracts(t, cases)
}

// TestTreeHelpRenderMatchesPi runs TreeHelp.render over every width with default, partly unbound, compacted, glyph-substituted and rebound keys.
func TestTreeHelpRenderMatchesPi(t *testing.T) {
	restoreTUIKeybindings(t)
	variants := map[string]map[string][]string{
		"default":                nil,
		"move-keys-unbound":      {"tui.select.up": {}, "tui.select.down": {}},
		"down-unbound":           {"tui.select.down": {}},
		"up-unbound":             {"tui.select.up": {}},
		"page-keys-unbound":      {"tui.editor.cursorLeft": {}, "tui.editor.cursorRight": {}},
		"branch-keys-unbound":    {"app.tree.foldOrUp": {}, "app.tree.unfoldOrDown": {}},
		"copy-unbound":           {"app.message.copy": {}},
		"label-unbound":          {"app.tree.editLabel": {}},
		"label-time-unbound":     {"app.tree.toggleLabelTimestamp": {}},
		"filters-unbound":        {"app.tree.filter.default": {}, "app.tree.filter.noTools": {}, "app.tree.filter.userOnly": {}, "app.tree.filter.labeledOnly": {}, "app.tree.filter.all": {}},
		"some-filters-unbound":   {"app.tree.filter.noTools": {}, "app.tree.filter.all": {}},
		"cycle-unbound":          {"app.tree.filter.cycleForward": {}, "app.tree.filter.cycleBackward": {}},
		"cycle-backward-unbound": {"app.tree.filter.cycleBackward": {}},
		"shared-modifier":        {"app.tree.foldOrUp": {"ctrl+x"}, "app.tree.unfoldOrDown": {"ctrl+y"}},
		"mixed-modifiers":        {"app.tree.foldOrUp": {"alt+x"}, "app.tree.unfoldOrDown": {"ctrl+y"}},
		"one-side-has-no-prefix": {"app.tree.foldOrUp": {"x"}, "app.tree.unfoldOrDown": {"ctrl+y"}},
		"no-prefix-either":       {"tui.select.up": {"k"}, "tui.select.down": {"j"}},
		"first-of-many":          {"tui.select.up": {"k", "up"}, "tui.select.down": {"j", "down"}},
		"page-words":             {"tui.editor.cursorLeft": {"pageUp"}, "tui.editor.cursorRight": {"pageDown"}},
		"shifted-arrows":         {"tui.select.up": {"shift+up"}, "tui.select.down": {"shift+down"}},
		"arrow-words":            {"tui.editor.cursorLeft": {"left"}, "tui.editor.cursorRight": {"right"}},
		"word-inside-a-key":      {"tui.select.up": {"upx"}, "tui.select.down": {"xdown"}},
		"function-keys":          {"app.message.copy": {"f5"}, "app.tree.editLabel": {"home"}, "app.tree.toggleLabelTimestamp": {"end"}},
		"long-modifier-chain":    {"app.message.copy": {"ctrl+shift+alt+c"}},
		"many-filter-keys":       {"app.tree.filter.default": {"ctrl+g"}, "app.tree.filter.noTools": {"ctrl+h"}, "app.tree.filter.userOnly": {"ctrl+j"}, "app.tree.filter.labeledOnly": {"ctrl+k"}, "app.tree.filter.all": {"ctrl+q"}},
		"filter-keys-shared":     {"app.tree.filter.default": {"ctrl+g"}, "app.tree.filter.noTools": {"ctrl+g"}, "app.tree.filter.userOnly": {"ctrl+g"}, "app.tree.filter.labeledOnly": {"ctrl+g"}, "app.tree.filter.all": {"ctrl+g"}},
		"wide-key-names":         {"app.message.copy": {"ctrl+alt+shift+f12"}, "app.tree.editLabel": {"ctrl+alt+shift+f11"}},
	}
	var cases []treeContractCase
	for name, bindings := range variants {
		for width := 0; width <= 140; width++ {
			cases = append(cases, treeContractCase{name: fmt.Sprintf("%s/w%03d", name, width), fixture: "fork", leaf: "b1u", width: width, zeroWidth: width == 0, bindings: bindings, check: wantWithinWidth(width)})
		}
		cases = append(cases, treeContractCase{name: name + "/light", fixture: "fork", leaf: "b1u", theme: "light", bindings: bindings})
	}
	runTreeContracts(t, cases)
}

// TestTreeLabelInputRenderMatchesPi opens the label editor over labels of every shape at many widths and with rebound confirm and cancel keys.
func TestTreeLabelInputRenderMatchesPi(t *testing.T) {
	typed := map[string][]string{
		"empty":     nil,
		"short":     {"a", "b"},
		"long":      {"abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789"},
		"wide":      {"宽字符日本語宽字符日本語宽字符日本語宽字符日本語"},
		"emoji":     {"😀😀😀"},
		"cursor-in": {"a", "b", "c", tkLeft},
		"prefilled": nil,
	}
	hints := map[string]map[string][]string{
		"default":        nil,
		"confirm-many":   {"tui.select.confirm": {"enter", "ctrl+y"}},
		"cancel-single":  {"tui.select.cancel": {"escape"}},
		"cancel-ctrl":    {"tui.select.cancel": {"ctrl+x", "ctrl+g"}},
		"confirm-ctrl-j": {"tui.select.confirm": {"ctrl+j"}},
	}
	var cases []treeContractCase
	for name, keys := range typed {
		leaf := "b1u"
		fixture := "fork"
		if name == "prefilled" {
			fixture, leaf = "wide", "w2"
		}
		script := append([]string{"L"}, keys...)
		for width := 0; width <= 80; width++ {
			cases = append(cases, treeContractCase{name: fmt.Sprintf("%s/w%03d", name, width), fixture: fixture, leaf: leaf, width: width, zeroWidth: width == 0, keys: script, check: wantWithinWidth(width)})
		}
		cases = append(cases, treeContractCase{name: name + "/light", fixture: fixture, leaf: leaf, theme: "light", keys: script})
		for hint, bindings := range hints {
			for _, width := range []int{20, 40, 100} {
				cases = append(cases, treeContractCase{name: fmt.Sprintf("%s/hints-%s/w%03d", name, hint, width), fixture: fixture, leaf: leaf, width: width, bindings: bindings, keys: script, check: wantWithinWidth(width)})
			}
		}
	}
	runTreeContracts(t, cases)
}
