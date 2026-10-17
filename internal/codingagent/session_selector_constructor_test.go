package codingagent

import (
	"context"
	"testing"
	"time"
)

// .upstream/current/packages/coding-agent/src/modes/interactive/components/session-selector.ts:800-813,757-777: the constructor takes
// (currentSessionsLoader, allSessionsLoader, onSelect, onCancel, onExit, requestRender, options, currentSessionFilePath); the confirm
// key runs onSelect with the highlighted path, the cancel key runs onCancel, onExit is the list's, and requestRender runs when the
// selector changes what it shows.
func TestSessionSelectorConstructorCallbacksMatchUpstream(t *testing.T) {
	var selected []string
	cancels, renders := 0, 0
	loader := func(context.Context, SessionListProgress) ([]SessionInfo, error) {
		return []SessionInfo{scopeSession("one")}, nil
	}
	exited := false
	selector := NewSessionSelectorComponent(loader, loader, func(path string) { selected = append(selected, path) }, func() { cancels++ }, func() { exited = true }, func() { renders++ }, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)}, "")
	defer selector.close()
	for deadline := time.Now().Add(5 * time.Second); len(selector.filtered) == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		selector.drainLoadUpdates()
	}
	selector.HandleInput("\r")
	if len(selected) != 1 || selected[0] != "/tmp/one.jsonl" {
		t.Fatalf("onSelect calls = %v", selected)
	}
	other := NewSessionSelectorComponent(loader, loader, nil, func() { cancels++ }, nil, nil, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)}, "")
	defer other.close()
	other.HandleInput("\x1b")
	if cancels != 1 {
		t.Fatalf("onCancel calls = %d", cancels)
	}
	selector.GetSessionList().OnExit()
	if !exited {
		t.Fatal("the list's onExit is the constructor's")
	}
	before := renders
	selector.toggleSortMode()
	selector.toggleNameFilter()
	if renders != before+2 {
		t.Fatalf("requestRender ran %d times for two toggles, want 2", renders-before)
	}
	selector.setStatusMessage("hello", false, 0)
	if renders != before+3 {
		t.Fatalf("a status message must request a render (renders %d)", renders-before)
	}
}
