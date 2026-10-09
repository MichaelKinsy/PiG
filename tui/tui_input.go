package tui

import (
	"io"
	"slices"
	"sync"
)

// Ports packages/tui/src/tui.ts

// TuiInputResult is what an input listener returns. Consume stops the input from reaching later listeners and the focused component. A non-nil Data replaces the input for later listeners and the focused component.
type TuiInputResult struct {
	Consume bool
	Data    *string
}

// TuiInputListener sees raw terminal input before the focused component. A nil result passes the input on unchanged. Go function values are not comparable, so listeners are registered by pointer; the pointer is the identity RemoveInputListener matches.
type TuiInputListener func(data string) *TuiInputResult

// rendererInput is the terminal-facing state of a renderer: input listeners and light/dark notifications.
type rendererInput struct {
	mu                  sync.Mutex
	listeners           []*TuiInputListener
	schemeListeners     []*func(TerminalColorScheme)
	schemeNotifications bool
}

// AddInputListener registers listener after the existing ones and returns the function that removes it.
func (t *tuiBase) AddInputListener(listener *TuiInputListener) func() {
	t.input.mu.Lock()
	if !containsPointer(t.input.listeners, listener) {
		t.input.listeners = append(t.input.listeners, listener)
	}
	t.input.mu.Unlock()
	return func() { t.RemoveInputListener(listener) }
}

// RemoveInputListener removes a registered listener. Removing one that is not registered does nothing.
func (t *tuiBase) RemoveInputListener(listener *TuiInputListener) {
	t.input.mu.Lock()
	defer t.input.mu.Unlock()
	for i, existing := range t.input.listeners {
		if existing == listener {
			t.input.listeners = append(t.input.listeners[:i:i], t.input.listeners[i+1:]...)
			return
		}
	}
}

func containsPointer[T any](list []*T, item *T) bool {
	return slices.Contains(list, item)
}

// RunInputListeners passes data through the registered listeners in registration order. It returns the data after any replacements and whether a listener consumed it or left it empty, in which case nothing else should receive it.
func (t *tuiBase) RunInputListeners(data string) (string, bool) {
	t.input.mu.Lock()
	listeners := slices.Clone(t.input.listeners)
	t.input.mu.Unlock()
	if len(listeners) == 0 {
		return data, false
	}
	current := data
	for _, listener := range listeners {
		result := (*listener)(current)
		if result == nil {
			continue
		}
		if result.Consume {
			return current, true
		}
		if result.Data != nil {
			current = *result.Data
		}
	}
	return current, current == ""
}

// OnTerminalColorSchemeChange registers a light/dark report listener and returns the function that removes it.
func (t *tuiBase) OnTerminalColorSchemeChange(listener func(scheme TerminalColorScheme)) func() {
	entry := &listener
	t.input.mu.Lock()
	t.input.schemeListeners = append(t.input.schemeListeners, entry)
	t.input.mu.Unlock()
	return func() {
		t.input.mu.Lock()
		defer t.input.mu.Unlock()
		for i, existing := range t.input.schemeListeners {
			if existing == entry {
				t.input.schemeListeners = append(t.input.schemeListeners[:i:i], t.input.schemeListeners[i+1:]...)
				return
			}
		}
	}
}

// SetTerminalColorSchemeNotifications turns the terminal's light/dark reports (mode 2031) on or off. While the renderer is stopped only the preference changes; Start applies it.
func (t *tuiBase) SetTerminalColorSchemeNotifications(enabled bool) {
	t.input.mu.Lock()
	if t.input.schemeNotifications == enabled {
		t.input.mu.Unlock()
		return
	}
	t.input.schemeNotifications = enabled
	t.input.mu.Unlock()
	if !t.stopped {
		writeColorSchemeNotifications(t.out, enabled)
	}
}

// colorSchemeNotificationsEnabled reports the stored preference.
func (t *tuiBase) colorSchemeNotificationsEnabled() bool {
	t.input.mu.Lock()
	defer t.input.mu.Unlock()
	return t.input.schemeNotifications
}

func writeColorSchemeNotifications(out io.Writer, enabled bool) {
	sequence := "\x1b[?2031l"
	if enabled {
		sequence = "\x1b[?2031h"
	}
	_, _ = io.WriteString(out, sequence)
}

// ConsumeTerminalColorSchemeReport notifies the listeners of a light/dark report and reports whether data was one.
func (t *tuiBase) ConsumeTerminalColorSchemeReport(data string) bool {
	scheme := ParseTerminalColorSchemeReport(data)
	if scheme == "" {
		return false
	}
	t.input.mu.Lock()
	listeners := slices.Clone(t.input.schemeListeners)
	t.input.mu.Unlock()
	for _, listener := range listeners {
		(*listener)(scheme)
	}
	return true
}

// HandleTerminalInput is the renderer's dispatcher for one chunk of terminal input, in the order of tui.ts:1044 handleTerminalInput: color replies and
// light/dark reports, the input listeners, the cell-size reply, then [tuiBase.DispatchFocusedInput].
func (t *tuiBase) HandleTerminalInput(data string) {
	if t.ConsumeTerminalColorResponse(data) || t.ConsumeTerminalColorSchemeReport(data) {
		return
	}
	data, done := t.RunInputListeners(data)
	if done || t.ConsumeCellSizeResponse(data) {
		return
	}
	t.DispatchFocusedInput(data)
}

// DispatchFocusedInput is the end of tui.ts:1044 handleTerminalInput, for a driver that has run the earlier stages itself: the global debug key
// (Shift+Ctrl+D) runs onDebug whatever has focus, then the overlay visibility and focus restoration are refreshed ([tuiBase.ActiveOverlay]), and the
// focused component's HandleInput receives the input, except a key release it does not ask for. Input is latency-sensitive, so a delivery requests an
// immediate render.
func (t *tuiBase) DispatchFocusedInput(data string) {
	if t.ConsumeDebugKey(data) {
		return
	}
	focused := t.ActiveOverlay()
	if focused == nil {
		focused = t.GetFocusedComponent()
	}
	input, ok := focused.(InputHandler)
	if !ok {
		return
	}
	if !ShouldDeliverKey(focused, data) {
		return
	}
	input.HandleInput(data)
	t.RequestImmediateRender()
}
