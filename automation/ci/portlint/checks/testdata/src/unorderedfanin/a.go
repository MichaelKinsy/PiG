package unorderedfanin

import (
	"maps"
	"slices"
	"sort"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type bridge struct {
	statuses map[string]string
	handlers map[string]func(string)
	ext      map[string]extension.Handler
	out      chan string
}

func (b *bridge) setStatus(key, text string) {}

func emitStatus(key string) {}

// Bad: a send per entry.
func (b *bridge) badSend() {
	for _, text := range b.statuses { // want `range over a map sends on a channel`
		b.out <- text
	}
}

// Bad: a callback stored in the map, called per entry.
func (b *bridge) badCallback() {
	for key, fn := range b.handlers { // want `range over a map invokes the callback fn`
		fn(key)
	}
}

// Bad: an extension interface method per entry.
func (b *bridge) badInterface() {
	for key, h := range b.ext { // want `range over a map calls extension.Handler.Handle`
		h.Handle(key)
	}
}

// Bad: a function named like a send.
func (b *bridge) badNamed() {
	for key := range b.statuses { // want `range over a map calls emitStatus`
		emitStatus(key)
	}
}

// Bad: a method named like a notification, through a named map type.
type registry map[string]int

func (r registry) badNotify(sink *bridge) {
	for key := range r { // want `range over a map calls notifyAll`
		notifyAll(sink, key)
	}
}

func notifyAll(*bridge, string) {}

// Good: keys sorted first.
func (b *bridge) goodSorted() {
	for _, key := range slices.Sorted(maps.Keys(b.statuses)) {
		b.out <- key
	}
	keys := make([]string, 0, len(b.statuses))
	for key := range b.statuses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		emitStatus(key)
	}
}

// Good: a map range that only collects or counts.
func (b *bridge) goodCollect() int {
	n := 0
	for range b.statuses {
		n++
	}
	copyOf := map[string]string{}
	for k, v := range b.statuses {
		copyOf[k] = v
	}
	return n
}

// Good: a send inside a closure the loop only builds is not the iteration's effect.
func (b *bridge) goodClosure() []func() {
	var fns []func()
	for _, text := range b.statuses {
		fns = append(fns, func() { b.out <- text })
	}
	return fns
}

// Good: ranging a slice or a channel is ordered.
func (b *bridge) goodSlice(keys []string) {
	for _, key := range keys {
		b.out <- key
	}
	for key := range b.out {
		emitStatus(key)
	}
}

// Good: the marker names its class.
func (b *bridge) goodMarked() {
	//portlint:allow unorderedfanin lifecycle: fails every pending request, nothing observes the order
	for key := range b.statuses {
		emitStatus(key)
	}
}
