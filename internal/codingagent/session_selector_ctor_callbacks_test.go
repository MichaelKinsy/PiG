package codingagent

import (
	"context"
	"testing"
	"time"
)

// upstream: session-selector.ts:757-815 the constructor takes onSelect, onCancel, onExit and requestRender; the list's
// select reaches onSelect(sessionPath) and cancel reaches onCancel (:800-809), and the status timer's expiry (clearStatusMessage -> setStatusMessage) calls
// requestRender (:121-125). The rename hint defaults to whether renameSession was passed (:773).
func TestSessionSelectorConstructorCallbacks(t *testing.T) {
	sessions := []SessionInfo{scopeSession("one"), scopeSession("two")}
	loader := SessionsLoader(func(context.Context, SessionListProgress) ([]SessionInfo, error) { return sessions, nil })
	var selected string
	cancels, renders := 0, 0
	s := NewSessionSelectorComponent(loader, loader,
		func(path string) { selected = path }, func() { cancels++ }, func() {}, func() { renders++ },
		&SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)}, "")
	defer s.close()
	for s.scopeLoad(sessionScopeCurrent) != nil {
		select {
		case update := <-s.work.updates:
			update()
		case result := <-s.loadResult(sessionScopeCurrent):
			s.finishLoad(sessionScopeCurrent, result)
		}
	}
	if len(s.filtered) != len(sessions) {
		t.Fatalf("sessions not loaded: %d", len(s.filtered))
	}
	s.HandleInput("\r")
	if selected != s.filtered[s.selected].Session.Path || selected == "" {
		t.Fatalf("onSelect got %q", selected)
	}
	s.setStatusMessage("note", false, time.Millisecond)
	var rendersBefore int
	select {
	case <-s.statusTimeout():
		rendersBefore = renders
		s.clearStatusMessage()
	case <-time.After(2 * time.Second):
		t.Fatal("status timer never fired")
	}
	if renders-rendersBefore != 1 {
		t.Fatalf("status expiry called requestRender %d times, want 1", renders-rendersBefore)
	}
	c := NewSessionSelectorComponent(loader, loader, nil, func() { cancels++ }, nil, nil, &SessionSelectorOptions{Keybindings: sessionSelectorInputBindings(t)}, "")
	defer c.close()
	c.HandleInput("\x1b")
	if cancels != 1 {
		t.Fatalf("onCancel called %d times, want 1", cancels)
	}
	if s.showRenameHint {
		t.Fatal("rename hint on without a renameSession")
	}
}
