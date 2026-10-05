package codingagent

import "github.com/MichaelKinsy/PiG/tui"

// modalContextDone is nil for component-only tests that do not run an interactive session.
func (m *InteractiveMode) modalContextDone() <-chan struct{} {
	if m.runCtx == nil {
		return nil
	}
	return m.runCtx.Done()
}

// modalStopped prevents nested selectors from admitting more input after their owner has ended.
func (m *InteractiveMode) modalStopped() bool {
	return m.requestExit.Load() || m.inputLoopErr != nil || m.runCtx != nil && m.runCtx.Err() != nil
}

// readModalInput services owner-loop tasks while a selector owns focus and releases that focus on shutdown or input failure.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (shutdown remains independent of selector focus).
func (m *InteractiveMode) readModalInput(input <-chan []byte) ([]byte, bool) {
	return waitModalValue(m, input, func(buf []byte) bool { return m.consumeModalHostInput(string(buf)) })
}

// consumeModalHostInput runs the input stages that precede a selector's own handler: terminal theme replies, then, in fullscreen, the alternate screen's viewport listener and the focused transcript search. Upstream TUI runs input listeners, including the alternate screen's handleViewportInput, before the focused component (tui-alt-screen.ts handleViewportInput; tui.ts handleInput), so PageUp, PageDown, ctrl+home, ctrl+end, the wheel and ctrl+shift+f move the viewport while an editor-slot selector has focus.
func (m *InteractiveMode) consumeModalHostInput(data string) bool {
	if m.consumeTerminalThemeInput(data) {
		return true
	}
	if m.altScreen == nil {
		return false
	}
	return m.altScreen.HandleViewportInput(data) || m.altScreen.HandleFocusedSearchInput(data)
}

// Ports packages/coding-agent/src/modes/interactive/components/session-selector.ts (confirmRename await).
// runModalOperation awaits owned storage work while the modal loop continues to service posted UI tasks and terminal render requests.
func (m *InteractiveMode) runModalOperation(work func() error) error {
	result := make(chan error, 1)
	m.backgroundTasks.Go(func() { result <- work() })
	value, ok := waitModalValue(m, result, nil)
	if !ok {
		// Storage work already admitted by the selector must settle before its owner unwinds.
		return <-result
	}
	return value
}

func waitModalValue[T any](m *InteractiveMode, input <-chan T, consume func(T) bool) (T, bool) {
	var zero T
	for {
		if m.modalStopped() {
			return zero, false
		}
		select {
		case <-m.modalContextDone():
			return zero, false
		case err := <-m.inputErrCh:
			m.inputLoopErr = err
			return zero, false
		case value, ok := <-input:
			if !ok || consume == nil || !consume(value) {
				return value, ok
			}
			m.tuiInst.Render()
		case task := <-m.uiTaskCh:
			task()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		}
	}
}

func (m *InteractiveMode) modalInputChunks(component tui.Component, chunks []string) []string {
	filtered := chunks[:0]
	for _, chunk := range chunks {
		if !m.consumeModalHostInput(chunk) {
			filtered = append(filtered, chunk)
		}
	}
	return dropKeyReleases(component, filtered)
}

func (m *InteractiveMode) dispatchModalInput(component tui.Component, chunks []string, handle func(string), done func() bool) {
	dispatchModalInput(component, m.modalInputChunks(component, chunks), handle, done)
}

func (m *InteractiveMode) drainModalInput(component tui.Component, input <-chan []byte, handle func(string) bool) {
	drainModalInput(component, input, func(chunk string) bool {
		if m.consumeModalHostInput(chunk) {
			return false
		}
		return handle(chunk)
	})
}
