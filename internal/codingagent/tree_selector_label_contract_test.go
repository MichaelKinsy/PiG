package codingagent

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// treeLabelInputCases are one obligation each of LabelInput.handleInput (tree-selector.ts:1310-1320) and of TreeSelectorComponent.handleInput
// (tree-selector.ts:1418-1424), which sends every key to the open label input and every other key to the tree list.
func treeLabelInputCases() []treeContractCase {
	open := "Label (empty to remove):"
	return []treeContractCase{
		// LabelInput.handleInput branch 1: tui.select.confirm
		{name: "label-confirm/saves-the-typed-label", fixture: "fork", leaf: "b1u", keys: []string{"L", "h", "i", tkEnter}, check: wantAll(wantEvents("label:b1u:hi"), wantFrameContains("[hi] "), wantFrameLacks(open))},
		{name: "label-confirm/saves-the-trimmed-label", fixture: "fork", leaf: "b1u", keys: []string{"L", " ", "h", "i", " ", " ", tkEnter}, check: wantAll(wantEvents("label:b1u:hi"), wantFrameContains("[hi] "))},
		{name: "label-confirm/trims-only-javascript-whitespace", fixture: "fork", leaf: "b1u", keys: []string{"L", "\x1b[200~\u0085x\ufeff\u00a0\x1b[201~", tkEnter}, check: wantEvents("label:b1u:\u0085x")},
		{name: "label-confirm/blank-input-removes-the-label", fixture: "mixed", leaf: "lab", keys: []string{"L", "\x05", tkBackspace, tkBackspace, tkBackspace, tkBackspace, tkEnter}, check: wantAll(wantEvents("label:lab:"), wantFrameLacks("[mark]"), wantFrameLacks(open))},
		{name: "label-confirm/whitespace-only-input-removes-the-label", fixture: "mixed", leaf: "lab", keys: []string{"L", "\x05", tkBackspace, tkBackspace, tkBackspace, tkBackspace, " ", " ", tkEnter}, check: wantAll(wantEvents("label:lab:"), wantFrameLacks("[mark]"))},
		{name: "label-confirm/unchanged-prefill-saves-it-again", fixture: "mixed", leaf: "lab", keys: []string{"L", tkEnter}, check: wantAll(wantEvents("label:lab:mark"), wantFrameContains("[mark] "))},
		{name: "label-confirm/blank-label-on-an-unlabeled-entry-reports-removal", fixture: "fork", leaf: "b1u", keys: []string{"L", tkEnter}, check: wantEvents("label:b1u:")},
		{name: "label-confirm/shows-the-list-again-on-the-same-row", fixture: "fork", leaf: "b1u", keys: []string{"L", "x", tkEnter}, check: wantAll(wantPositionAndRow("(5/8)", "[x] "), wantFrameLacks(open))},
		{name: "label-confirm/does-not-select-or-cancel", fixture: "fork", leaf: "b1u", keys: []string{"L", "x", tkEnter, tkEnter}, check: wantEvents("label:b1u:x", "select:b1u")},
		{name: "label-confirm/rebound-confirm-key-saves", fixture: "fork", leaf: "b1u", bindings: map[string][]string{"tui.select.confirm": {"ctrl+y"}}, keys: []string{"L", "x", "\x19"}, check: wantEvents("label:b1u:x")},
		{name: "label-confirm/rebound-confirm-key-leaves-enter-to-the-input", fixture: "fork", leaf: "b1u", bindings: map[string][]string{"tui.select.confirm": {"ctrl+y"}}, keys: []string{"L", "x", tkEnter}, check: wantAll(wantEvents(), wantFrameContains(open))},
		{name: "label-confirm/saved-label-is-searchable", fixture: "fork", leaf: "b1u", keys: []string{"L", "z", "q", tkEnter, "z", "q"}, check: wantTotal(1)},
		{name: "label-confirm/saved-label-feeds-labeled-only", fixture: "fork", leaf: "b1u", keys: []string{"L", "x", tkEnter, tkCtrlL}, check: wantPosition("(1/1) [labeled]")},
		{name: "label-confirm/labeled-only-view-is-not-refiltered-by-a-save", fixture: "mixed", leaf: "lab", filter: "labeled-only", keys: []string{"L", "\x05", tkBackspace, tkBackspace, tkBackspace, tkBackspace, tkEnter}, check: wantAll(wantEvents("label:lab:"), wantPosition("(1/1) [labeled]"))},
		{name: "label-confirm/labeled-only-view-refilters-on-the-next-action", fixture: "mixed", leaf: "lab", filter: "labeled-only", keys: []string{"L", "\x05", tkBackspace, tkBackspace, tkBackspace, tkBackspace, tkEnter, tkCtrlD}, check: wantPosition("(5/5)")},
		// LabelInput.handleInput branch 2: tui.select.cancel
		{name: "label-cancel/hides-the-input-without-saving", fixture: "mixed", leaf: "lab", keys: []string{"L", "x", "y", tkEscape}, check: wantAll(wantEvents(), wantFrameLacks(open), wantFrameContains("[mark] "), wantPosition("(5/5)"))},
		{name: "label-cancel/does-not-cancel-the-selector", fixture: "fork", leaf: "b1u", keys: []string{"L", tkEscape}, check: wantEvents()},
		{name: "label-cancel/second-press-cancels-the-selector", fixture: "fork", leaf: "b1u", keys: []string{"L", tkEscape, tkEscape}, check: wantEvents("cancel")},
		{name: "label-cancel/ctrl-c-is-the-second-default-cancel-key", fixture: "fork", leaf: "b1u", keys: []string{"L", "\x03"}, check: wantAll(wantEvents(), wantFrameLacks(open))},
		{name: "label-cancel/rebound-cancel-key", fixture: "fork", leaf: "b1u", bindings: map[string][]string{"tui.select.cancel": {"ctrl+x"}}, keys: []string{"L", "\x18"}, check: wantAll(wantEvents(), wantFrameLacks(open))},
		{name: "label-cancel/escape-after-rebinding-edits-instead", fixture: "fork", leaf: "b1u", bindings: map[string][]string{"tui.select.cancel": {"ctrl+x"}}, keys: []string{"L", tkEscape}, check: wantFrameContains(open)},
		{name: "label-cancel/reopening-starts-from-the-unchanged-label", fixture: "mixed", leaf: "lab", keys: []string{"L", "x", tkEscape, "L"}, check: wantFrameContains(open, "mark")},
		// LabelInput.handleInput branch 3: everything else edits
		{name: "label-edit/typing-appends", fixture: "fork", leaf: "b1u", keys: []string{"L", "a", "b", "c"}, check: wantFrameContains("abc")},
		{name: "label-edit/typing-does-not-reach-the-search", fixture: "fork", leaf: "b1u", keys: []string{"L", "a", "b"}, check: wantAll(wantSearch("  Type to search:"), wantFrameContains(open))},
		{name: "label-edit/backspace-deletes", fixture: "fork", leaf: "b1u", keys: []string{"L", "a", "b", tkBackspace}, check: wantFrameContains("> a")},
		{name: "label-edit/cursor-movement-inserts-in-the-middle", fixture: "fork", leaf: "b1u", keys: []string{"L", "a", "b", tkLeft, "x", tkEnter}, check: wantEvents("label:b1u:axb")},
		{name: "label-edit/home-and-end-move-the-cursor", fixture: "fork", leaf: "b1u", keys: []string{"L", "a", "b", "\x01", "x", "\x05", "y", tkEnter}, check: wantEvents("label:b1u:xaby")},
		{name: "label-edit/kill-to-end-and-yank", fixture: "fork", leaf: "b1u", keys: []string{"L", "a", "b", "c", tkLeft, tkLeft, "\x0b", "\x19", "\x19", tkEnter}, check: wantEvents("label:b1u:abcbc")},
		{name: "label-edit/delete-word-backward", fixture: "fork", leaf: "b1u", keys: []string{"L", "o", "n", "e", " ", "t", "w", "o", "\x17", tkEnter}, check: wantEvents("label:b1u:one")},
		{name: "label-edit/bracketed-paste", fixture: "fork", leaf: "b1u", keys: []string{"L", "\x1b[200~pasted\x1b[201~", tkEnter}, check: wantEvents("label:b1u:pasted")},
		{name: "label-edit/arrow-keys-do-not-move-the-list", fixture: "fork", leaf: "b1u", keys: []string{"L", tkUp, tkDown, tkPageUp, tkPageDown, tkEscape}, check: wantPosition("(5/8)")},
		{name: "label-edit/tree-hotkeys-are-typed-not-run", fixture: "fork", leaf: "b1u", keys: []string{"L", tkCtrlT, tkCtrlO, "T", "L", tkEscape}, check: wantAll(wantEvents(), wantPosition("(5/8)"))},
		{name: "label-edit/fold-key-does-not-fold", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{"L", tkCtrlLeft, tkEscape}, check: wantAll(wantPosition("(1/8)"), wantFrameLacks("⊞"))},
		{name: "label-edit/copy-key-is-not-a-copy", fixture: "fork", leaf: "b1u", keys: []string{"L", tkCopy, tkEscape}, check: wantEvents()},
		// TreeSelectorComponent.handleInput routing
		{name: "route/list-receives-keys-while-no-label-input-is-open", fixture: "fork", leaf: "b1u", keys: []string{tkDown}, check: wantPosition("(6/8)")},
		{name: "route/label-input-receives-keys-while-open", fixture: "fork", leaf: "b1u", keys: []string{"L", tkDown, "x", tkEnter}, check: wantEvents("label:b1u:x")},
		{name: "route/list-receives-keys-again-after-a-save", fixture: "fork", leaf: "b1u", keys: []string{"L", "x", tkEnter, tkDown}, check: wantPosition("(6/8)")},
		{name: "route/list-receives-keys-again-after-a-cancel", fixture: "fork", leaf: "b1u", keys: []string{"L", tkEscape, tkDown}, check: wantPosition("(6/8)")},
		{name: "route/label-input-opens-on-the-highlighted-row", fixture: "fork", leaf: "b1u", keys: []string{tkUp, tkUp, "L", "x", tkEnter}, check: wantEvents("label:b1:x")},
		{name: "route/label-input-reopens-after-a-save", fixture: "fork", leaf: "b1u", keys: []string{"L", "x", tkEnter, tkUp, "L", "y", tkEnter}, check: wantEvents("label:b1u:x", "label:b1a:y")},
		{name: "route/header-stays-while-the-label-input-is-open", fixture: "fork", leaf: "b1u", keys: []string{"L"}, check: wantFrameContains("Session Tree", "Type to search:", "ctrl+x copy")},
		{name: "route/label-input-draws-the-cursor-marker-when-focused", fixture: "fork", leaf: "b1u", keys: []string{"L"}, check: func(t *testing.T, tr treeContractTrace) {
			t.Helper()
			if !strings.Contains(strings.Join(tr.lastFrame(), "\n"), "\x1b_pi:c\x07") {
				t.Errorf("focused label input has no cursor marker:\n%s", strings.Join(tr.lastFrame(), "\n"))
			}
		}},
		{name: "route/empty-tree-opens-no-label-input", fixture: "empty", keys: []string{"L", "x"}, check: wantAll(wantFrameLacks(open), wantSearch("  Type to search: x"))},
	}
}

func TestTreeLabelInputAndRoutingContractsMatchPi(t *testing.T) {
	restoreStartupTheme(t)
	runTreeContracts(t, treeLabelInputCases())
}

// Pi's updateNodeLabel stamps a saved label with new Date().toISOString() and clears the stamp when the label is removed (tree-selector.ts:639-647).
// Pi renders the stamp only while the timestamp toggle is on, so the clock is the independent denominator here.
func TestTreeSavedLabelCarriesTheSaveTimeAndRemovalClearsIt(t *testing.T) {
	restoreStartupTheme(t)
	previousKeys, previousCaps, previousTheme := tui.GetTUIKeybindings(), tui.GetCapabilities(), tui.ActiveTheme()
	t.Cleanup(func() {
		tui.SetTUIKeybindings(previousKeys)
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
	})
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	DefaultKeybindingsManager().syncToTUI()
	build := func() *tui.TreeSelectorComponent {
		roots := []*SessionTreeNode{{Entry: mustEntry(t, map[string]any{"type": "message", "id": "e1", "parentId": nil, "timestamp": "2018-02-01T10:00:00.000Z", "message": map[string]any{"role": "user", "content": "entry text", "timestamp": 1517479200000}})}}
		leaf := "e1"
		return NewTreeSelectorComponent(roots, &leaf, 40, nil, nil, func(string, *string) {}, nil, "")
	}
	clockTimes := func(from time.Time) []string {
		return []string{from.Format("15:04"), from.Add(time.Minute).Format("15:04"), from.Add(-time.Minute).Format("15:04")}
	}
	selector := build()
	selector.HandleInput("T")
	for _, key := range []string{"L", "n", "o", "t", "e", "\r"} {
		selector.HandleInput(key)
	}
	saved := treeSelectedRow(selector.Render(100))
	matched := false
	for _, hhmm := range clockTimes(time.Now()) {
		if strings.Contains(saved, "[note] "+hhmm+" ") {
			matched = true
		}
	}
	if !matched {
		t.Errorf("saved label row %q lacks the save time next to [note]", saved)
	}
	// Removing the label clears its stamp: no label, no time.
	for _, key := range []string{"L", "\x05", tkBackspace, tkBackspace, tkBackspace, tkBackspace, "\r"} {
		selector.HandleInput(key)
	}
	removed := treeSelectedRow(selector.Render(100))
	if strings.Contains(removed, "[") || regexp.MustCompile(`\d\d:\d\d`).MatchString(removed) {
		t.Errorf("row after removing the label = %q, want no label and no time", removed)
	}
	// A label the toggle shows later carries the same stamp.
	later := build()
	for _, key := range []string{"L", "n", "\r", "T"} {
		later.HandleInput(key)
	}
	matched = false
	for _, hhmm := range clockTimes(time.Now()) {
		if strings.Contains(treeSelectedRow(later.Render(100)), "[n] "+hhmm+" ") {
			matched = true
		}
	}
	if !matched {
		t.Errorf("toggling the time on after saving shows %q", treeSelectedRow(later.Render(100)))
	}
}

// Pi's SearchLine.handleInput is empty and no container routes input to it, so the search line takes no part in input.
func TestTreeSearchLineTakesNoInput(t *testing.T) {
	selector := NewTreeSelectorComponent(nil, nil, 40, nil, nil, nil, nil, "")
	handlers := 0
	for _, child := range selector.Children() {
		if _, ok := child.(interface{ HandleInput(string) }); ok {
			handlers++
		}
	}
	if handlers != 0 {
		t.Errorf("%d children of the tree selector take input; Pi's children (spacer, border, text, help, search line, containers) take none", handlers)
	}
}

// TreeSelectorComponent.capturesViewportInput is true exactly while no label input is open (tree-selector.ts:1414-1416).
func TestTreeSelectorCapturesViewportInputExceptWhileLabelling(t *testing.T) {
	previousKeys := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previousKeys) })
	DefaultKeybindingsManager().syncToTUI()
	leaf := "e1"
	roots := []*SessionTreeNode{{Entry: mustEntry(t, map[string]any{"type": "message", "id": "e1", "parentId": nil, "timestamp": "2018-02-01T10:00:00.000Z", "message": map[string]any{"role": "user", "content": "entry text", "timestamp": 1517479200000}})}}
	selector := NewTreeSelectorComponent(roots, &leaf, 40, nil, nil, nil, nil, "")
	steps := []struct {
		key  string
		want bool
	}{{"", true}, {"L", false}, {"x", false}, {tkEscape, true}, {"L", false}, {"\r", true}}
	for _, step := range steps {
		if step.key != "" {
			selector.HandleInput(step.key)
		}
		if got := selector.CapturesViewportInput(); got != step.want {
			t.Fatalf("after %q CapturesViewportInput = %v, want %v", step.key, got, step.want)
		}
	}
}
