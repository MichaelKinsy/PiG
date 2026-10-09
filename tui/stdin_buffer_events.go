// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package tui

// Ports packages/tui/src/stdin-buffer.ts: StdinBuffer extends EventEmitter<StdinBufferEventMap> and delivers its input through the
// "data" and "paste" events. The listener surface follows Node's EventEmitter (lib/events.js).

import (
	"slices"
)

// StdinBufferEvent names an event of a StdinBuffer (stdin-buffer.ts StdinBufferEventMap).
type StdinBufferEvent string

// The events a StdinBuffer emits.
const (
	// StdinBufferEventData carries one complete input sequence.
	StdinBufferEventData StdinBufferEvent = "data"
	// StdinBufferEventPaste carries the content of one bracketed paste, without the paste markers.
	StdinBufferEventPaste StdinBufferEvent = "paste"
)

// StdinBufferListener gives a Go callback the stable identity a JavaScript function has: Off and ListenerCount find a listener by it.
type StdinBufferListener struct {
	call func(data string)
	// original is the listener a once registration wraps (the wrapper's `listener` property in Node); nil for a plain registration.
	original *StdinBufferListener
	once     bool
}

// NewStdinBufferListener wraps call as a listener.
func NewStdinBufferListener(call func(data string)) *StdinBufferListener {
	return &StdinBufferListener{call: call}
}

// unwrapped is the listener a registration stands for: a once wrapper's original, else the registration itself.
func (l *StdinBufferListener) unwrapped() *StdinBufferListener {
	if l.original != nil {
		return l.original
	}
	return l
}

// stdinListeners is the EventEmitter state of a StdinBuffer. The zero value is an emitter with no listeners.
// Like the rest of the StdinBuffer it is not safe for concurrent use.
type stdinListeners struct {
	order   []StdinBufferEvent
	byEvent map[StdinBufferEvent][]*StdinBufferListener
}

// addRegistration appends or prepends registration l for event (EventEmitter._addListener without the newListener event).
func (b *StdinBuffer) addRegistration(event StdinBufferEvent, l *StdinBufferListener, prepend bool) *StdinBuffer {
	if l == nil || l.call == nil {
		return b
	}
	if b.events.byEvent == nil {
		b.events.byEvent = map[StdinBufferEvent][]*StdinBufferListener{}
	}
	existing, ok := b.events.byEvent[event]
	if !ok {
		b.events.order = append(b.events.order, event)
	}
	if prepend {
		b.events.byEvent[event] = append([]*StdinBufferListener{l}, existing...)
	} else {
		b.events.byEvent[event] = append(existing, l)
	}
	return b
}

// once wraps l so that the first emit removes the registration before calling l (EventEmitter.once).
func (b *StdinBuffer) onceWrapper(event StdinBufferEvent, l *StdinBufferListener) *StdinBufferListener {
	var wrapper *StdinBufferListener
	wrapper = &StdinBufferListener{original: l, once: true, call: func(data string) {
		b.removeRegistration(event, wrapper)
		l.call(data)
	}}
	return wrapper
}

// removeRegistration removes registration r from event's list and drops the event when the list empties.
func (b *StdinBuffer) removeRegistration(event StdinBufferEvent, r *StdinBufferListener) {
	list := b.events.byEvent[event]
	i := slices.Index(list, r)
	if i < 0 {
		return
	}
	b.dropAt(event, list, i)
}

func (b *StdinBuffer) dropAt(event StdinBufferEvent, list []*StdinBufferListener, i int) {
	rest := slices.Concat(list[:i], list[i+1:])
	if len(rest) == 0 {
		delete(b.events.byEvent, event)
		b.events.order = slices.DeleteFunc(b.events.order, func(e StdinBufferEvent) bool { return e == event })
		return
	}
	b.events.byEvent[event] = rest
}

// AddListener appends listener to the listeners of event and returns the buffer (EventEmitter.addListener). A nil listener is ignored.
func (b *StdinBuffer) AddListener(event StdinBufferEvent, listener *StdinBufferListener) *StdinBuffer {
	return b.addRegistration(event, listener, false)
}

// On is AddListener (EventEmitter.on).
func (b *StdinBuffer) On(event StdinBufferEvent, listener *StdinBufferListener) *StdinBuffer {
	return b.addRegistration(event, listener, false)
}

// Once appends a listener that the first emit of event removes before calling it (EventEmitter.once).
func (b *StdinBuffer) Once(event StdinBufferEvent, listener *StdinBufferListener) *StdinBuffer {
	if listener == nil || listener.call == nil {
		return b
	}
	return b.addRegistration(event, b.onceWrapper(event, listener), false)
}

// PrependListener adds listener ahead of the other listeners of event (EventEmitter.prependListener).
func (b *StdinBuffer) PrependListener(event StdinBufferEvent, listener *StdinBufferListener) *StdinBuffer {
	return b.addRegistration(event, listener, true)
}

// PrependOnceListener adds a once listener ahead of the other listeners of event (EventEmitter.prependOnceListener).
func (b *StdinBuffer) PrependOnceListener(event StdinBufferEvent, listener *StdinBufferListener) *StdinBuffer {
	if listener == nil || listener.call == nil {
		return b
	}
	return b.addRegistration(event, b.onceWrapper(event, listener), true)
}

// RemoveListener removes the most recently added registration of listener from event, a once registration included
// (EventEmitter.removeListener). A listener that is not registered leaves the buffer unchanged.
func (b *StdinBuffer) RemoveListener(event StdinBufferEvent, listener *StdinBufferListener) *StdinBuffer {
	if listener == nil {
		return b
	}
	list := b.events.byEvent[event]
	for i, l := range slices.Backward(list) {
		if l == listener || l.original == listener {
			b.dropAt(event, list, i)
			break
		}
	}
	return b
}

// Off is RemoveListener (EventEmitter.off).
func (b *StdinBuffer) Off(event StdinBufferEvent, listener *StdinBufferListener) *StdinBuffer {
	return b.RemoveListener(event, listener)
}

// RemoveAllListeners removes every listener of the given events, or of every event when none is given
// (EventEmitter.removeAllListeners).
func (b *StdinBuffer) RemoveAllListeners(events ...StdinBufferEvent) *StdinBuffer {
	if len(events) == 0 {
		b.events.byEvent = nil
		b.events.order = nil
		return b
	}
	for _, event := range events {
		if _, ok := b.events.byEvent[event]; ok {
			delete(b.events.byEvent, event)
			b.events.order = slices.DeleteFunc(b.events.order, func(e StdinBufferEvent) bool { return e == event })
		}
	}
	return b
}

// Emit calls the listeners of event in order with data and reports whether there were any (EventEmitter.emit). It calls a snapshot
// of the list: a listener added during the emit waits for the next one, and one removed during it is still called.
func (b *StdinBuffer) Emit(event StdinBufferEvent, data string) bool {
	list := slices.Clone(b.events.byEvent[event])
	for _, l := range list {
		l.call(data)
	}
	return len(list) > 0
}

// EventNames lists the events that have listeners, in the order their first listener was added (EventEmitter.eventNames).
func (b *StdinBuffer) EventNames() []StdinBufferEvent {
	return slices.Clone(b.events.order)
}

// ListenerCount counts the registrations of event, or only those of listener when one is given (EventEmitter.listenerCount).
func (b *StdinBuffer) ListenerCount(event StdinBufferEvent, listener ...*StdinBufferListener) int {
	list := b.events.byEvent[event]
	if len(listener) == 0 || listener[0] == nil {
		return len(list)
	}
	n := 0
	for _, l := range list {
		if l == listener[0] || l.original == listener[0] {
			n++
		}
	}
	return n
}

// Listeners returns the listeners of event as the caller added them: a once registration appears as its original
// (EventEmitter.listeners).
func (b *StdinBuffer) Listeners(event StdinBufferEvent) []*StdinBufferListener {
	list := b.events.byEvent[event]
	out := make([]*StdinBufferListener, len(list))
	for i, l := range list {
		out[i] = l.unwrapped()
	}
	return out
}

// RawListeners returns the registrations of event, a once registration as its wrapper; Original on the wrapper is the listener it
// stands for (EventEmitter.rawListeners).
func (b *StdinBuffer) RawListeners(event StdinBufferEvent) []*StdinBufferListener {
	return slices.Clone(b.events.byEvent[event])
}

// Original is the listener a once registration wraps, or nil for a plain registration (the `listener` property of the wrapper
// EventEmitter.rawListeners returns).
func (l *StdinBufferListener) Original() *StdinBufferListener { return l.original }
