package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts init (974-1152): the isInitialized guard.

import "testing"

// init() returns at once when the mode is initialized: it registers no signal handlers, builds no UI and binds nothing a second time.
func TestInitReturnsWithoutWorkOnceInitialized(t *testing.T) {
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir()})
	m.isInitialized = true
	if err := m.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.runCtx != nil || m.backgroundCancel != nil || m.tuiInst != nil || m.editor != nil {
		t.Fatal("Init set up an initialized mode again")
	}
}
