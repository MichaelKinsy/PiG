package codingagent

import (
	"context"
	"testing"
)

// upstream: packages/coding-agent/src/modes/interactive/components/session-selector.ts:729-741 `set focused` stores the flag and propagates it to the session list's search input and the rename input; `get focused` returns the stored flag.
func TestSessionSelectorFocusStoresAndPropagates(t *testing.T) {
	loader := func(context.Context, SessionListProgress) ([]SessionInfo, error) {
		return []SessionInfo{scopeSession("current")}, nil
	}
	selector := NewSessionSelectorComponent(loader, loader, nil, nil, nil, nil, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)}, "")
	defer selector.close()
	selector.SetFocused(false)
	if selector.searchInput.Focused || selector.renameInput.Focused {
		t.Fatal("SetFocused(false) left an input focused")
	}
	selector.SetFocused(true)
	if !selector.Focused() || !selector.searchInput.Focused || !selector.renameInput.Focused {
		t.Fatalf("after SetFocused(true): focused=%v search=%v rename=%v", selector.Focused(), selector.searchInput.Focused, selector.renameInput.Focused)
	}
}
