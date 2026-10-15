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
	listeners           liveSet[TuiInputListener]
	schemeListeners     liveSet[func(TerminalColorScheme)]
	schemeNotifications bool
}

// liveSet is an insertion-ordered set of pointers that iterates live, as a JavaScript Set does (tui.ts inputListeners and terminalColorSchemeListeners are Sets iterated with for...of): an entry added during an iteration is visited by it, and an entry removed before the iteration reaches it is not. A removal during an iteration leaves a tombstone, so the positions of the entries the iteration has yet to visit stay put; the last iteration to finish drops the tombstones. The owner's mutex guards every field.
type liveSet[T any] struct {
	entries   []*liveSetEntry[T]
	iterating int
}

type liveSetEntry[T any] struct {
	value   *T
	removed bool
}

// add appends value unless it is already a member.
func (s *liveSet[T]) add(value *T) {
	if s.index(value) < 0 {
		s.entries = append(s.entries, &liveSetEntry[T]{value: value})
	}
}

// remove removes value; removing a value that is not a member does nothing.
func (s *liveSet[T]) remove(value *T) {
	i := s.index(value)
	if i < 0 {
		return
	}
	if s.iterating > 0 {
		s.entries[i].removed = true
		return
	}
	s.entries = slices.Delete(s.entries, i, i+1)
}

func (s *liveSet[T]) index(value *T) int {
	return slices.IndexFunc(s.entries, func(e *liveSetEntry[T]) bool { return !e.removed && e.value == value })
}

// each calls visit for each member in insertion order until visit returns false. It holds mu only between calls, so visit may add and remove members.
func (s *liveSet[T]) each(mu *sync.Mutex, visit func(*T) bool) {
	mu.Lock()
	s.iterating++
	mu.Unlock()
	defer func() {
		mu.Lock()
		if s.iterating--; s.iterating == 0 {
			s.entries = slices.DeleteFunc(s.entries, func(e *liveSetEntry[T]) bool { return e.removed })
		}
		mu.Unlock()
	}()
	for i := 0; ; i++ {
		mu.Lock()
		if i >= len(s.entries) {
			mu.Unlock()
			return
		}
		entry := s.entries[i]
		removed := entry.removed
		mu.Unlock()
		if !removed && !visit(entry.value) {
			return
		}
	}
}

// size returns the number of members.
func (s *liveSet[T]) size() int {
	n := 0
	for _, e := range s.entries {
		if !e.removed {
			n++
		}
	}
	return n
}

// AddInputListener registers listener after the existing ones and returns the function that removes it.
func (t *tuiBase) AddInputListener(listener *TuiInputListener) func() {
	t.input.mu.Lock()
	t.input.listeners.add(listener)
	t.input.mu.Unlock()
	return func() { t.RemoveInputListener(listener) }
}

// RemoveInputListener removes a registered listener. Removing one that is not registered does nothing.
func (t *tuiBase) RemoveInputListener(listener *TuiInputListener) {
	t.input.mu.Lock()
	defer t.input.mu.Unlock()
	t.input.listeners.remove(listener)
}

// RunInputListeners passes data through the registered listeners in registration order. It returns the data after any replacements and whether a listener consumed it or left it empty, in which case nothing else should receive it.
// pig divergence (D105): the driver owns the terminal reader; extension input listeners run asynchronously around the renderer's listener chain.
func (t *tuiBase) RunInputListeners(data string) (string, bool) {
	t.input.mu.Lock()
	empty := t.input.listeners.size() == 0
	t.input.mu.Unlock()
	if empty {
		return data, false
	}
	current, consumed := data, false
	t.input.listeners.each(&t.input.mu, func(listener *TuiInputListener) bool {
		result := (*listener)(current)
		if result == nil {
			return true
		}
		if result.Consume {
			consumed = true
			return false
		}
		if result.Data != nil {
			current = *result.Data
		}
		return true
	})
	return current, consumed || current == ""
}

// OnTerminalColorSchemeChange registers a light/dark report listener and returns the function that removes it.
func (t *tuiBase) OnTerminalColorSchemeChange(listener func(scheme TerminalColorScheme)) func() {
	entry := &listener
	t.input.mu.Lock()
	t.input.schemeListeners.add(entry)
	t.input.mu.Unlock()
	return func() {
		t.input.mu.Lock()
		defer t.input.mu.Unlock()
		t.input.schemeListeners.remove(entry)
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
	t.input.schemeListeners.each(&t.input.mu, func(listener *func(TerminalColorScheme)) bool {
		(*listener)(scheme)
		return true
	})
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
