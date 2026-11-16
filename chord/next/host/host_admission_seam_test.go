package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/internal/admitseam"
)

// The test seam of an attempt's admission hop (internal/admitseam): unset, the hop is exactly a Queue call; held, it waits for
// the test.
func TestAdmitseamHold(t *testing.T) {
	newAttempt := func(t *testing.T) (*chordHost, *attempt) {
		t.Helper()
		h := newHost(t, HostOptions{OnError: func(HostError) {}}).(*chordHost)
		return h, &attempt{object: &chordObject{host: h}}
	}

	t.Run("an unheld hop queues as a plain microtask: before a job queued after it, after one queued before", func(t *testing.T) {
		h, a := newAttempt(t)
		var order []string
		h.enter(func() {
			h.loop().Queue(func() { order = append(order, "first") })
			a.admissionHop(func() { order = append(order, "hop") })
			h.loop().Queue(func() { order = append(order, "second") })
		})
		expectStrings(t, order, "first", "hop", "second")
		must(t, h.Close(background))
	})

	t.Run("a held hop runs when its start is called, from any goroutine, and not before", func(t *testing.T) {
		h, a := newAttempt(t)
		held := make(chan func(), 1)
		admitseam.Hold(h, func(start func()) { held <- start })
		ran := make(chan struct{})
		var order []string
		var section bool
		h.enter(func() {
			a.admissionHop(func() { order = append(order, "hop"); section = h.loop().(*Loop).InSection(); close(ran) })
			h.loop().Queue(func() { order = append(order, "second") })
		})
		expectStrings(t, order, "second")
		start := receive(t, held)
		go start()
		receive(t, ran)
		h.enter(func() {})
		expectStrings(t, order, "second", "hop")
		// Handed over, it still runs as the host's microtask: a section of its loop, not on the goroutine that started it.
		if !section {
			t.Error("the held hop ran outside a section of the host's loop")
		}
		must(t, h.Close(background))
	})

	t.Run("Hold(host, nil) ends it: the next hop queues as before", func(t *testing.T) {
		h, a := newAttempt(t)
		admitseam.Hold(h, func(start func()) { t.Error("a hop was handed to a seam that was ended") })
		admitseam.Hold(h, nil)
		var order []string
		h.enter(func() {
			a.admissionHop(func() { order = append(order, "hop") })
			h.loop().Queue(func() { order = append(order, "second") })
		})
		expectStrings(t, order, "hop", "second")
		must(t, h.Close(background))
	})
}
