package extension_test

// pi: packages/coding-agent/src/core/event-bus.ts

import (
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// event-bus.ts:30-32: clear removes every listener, and the bus keeps working afterwards.
func TestCreateEventBusClearRemovesEveryListener(t *testing.T) {
	bus := extension.CreateEventBus()
	called := 0
	off := bus.On("a", func(any) { called++ })
	bus.On("b", func(any) { called++ })
	bus.Clear()
	bus.Emit("a", nil)
	bus.Emit("b", nil)
	off()
	if called != 0 {
		t.Fatalf("listeners survived Clear: %d calls", called)
	}
	bus.On("a", func(any) { called++ })
	bus.Emit("a", nil)
	if called != 1 {
		t.Fatalf("the bus must keep working after Clear, calls = %d", called)
	}
}

// Pi core/event-bus.ts createEventBus: an EventEmitter whose handlers run synchronously in registration order,
// whose failures are reported as `Event handler error (<channel>):` and whose `on` returns an unsubscribe.
func TestCreateEventBusMatchesUpstream(t *testing.T) {
	bus := extension.CreateEventBus()
	var order []string
	offA := bus.On("c", func(data any) { order = append(order, "a:"+data.(string)) })
	bus.On("c", func(data any) { order = append(order, "b:"+data.(string)) })
	bus.On("other", func(any) { order = append(order, "other") })
	bus.Emit("c", "1")
	if !slices.Equal(order, []string{"a:1", "b:1"}) {
		t.Fatalf("order = %v", order)
	}
	offA()
	offA() // idempotent
	order = nil
	bus.Emit("c", "2")
	if !slices.Equal(order, []string{"b:2"}) {
		t.Fatalf("after unsubscribe = %v", order)
	}
	bus.Emit("nobody", nil)

	// A handler that subscribes or unsubscribes during an emit leaves that emit's recipients unchanged (Node clones the listener array).
	order = nil
	var offLate func()
	bus.On("snap", func(any) {
		order = append(order, "first")
		bus.On("snap", func(any) { order = append(order, "late") })
	})
	offLate = bus.On("snap", func(any) { order = append(order, "second") })
	_ = offLate
	bus.Emit("snap", nil)
	if !slices.Equal(order, []string{"first", "second"}) {
		t.Fatalf("snapshot = %v", order)
	}

	bus.Clear()
	order = nil
	bus.Emit("c", "3")
	bus.Emit("snap", nil)
	if len(order) != 0 {
		t.Fatalf("after Clear = %v", order)
	}
}

func TestCreateEventBusReportsHandlerFailureAndContinues(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	bus := extension.CreateEventBus()
	var after bool
	bus.On("boom", func(any) { panic("kaput") })
	bus.On("boom", func(any) { after = true })
	bus.Emit("boom", nil)
	os.Stderr = orig
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if !after {
		t.Fatal("a failing handler stopped the next listener")
	}
	if !strings.Contains(string(out), "Event handler error (boom): kaput") {
		t.Fatalf("stderr = %q", out)
	}
}

// Pi createEventBus over Node EventEmitter: emit clones the listener array, so a listener unsubscribed by an earlier listener during an emit still receives that emit and only later emits skip it. Pi probe (pi-coding-agent createEventBus, same sequence): "a,b,c,|,a,c".
func TestCreateEventBusUnsubscribeDuringEmitKeepsRecipients(t *testing.T) {
	bus := extension.CreateEventBus()
	var order []string
	var offB func()
	bus.On("x", func(any) { order = append(order, "a"); offB() })
	offB = bus.On("x", func(any) { order = append(order, "b") })
	bus.On("x", func(any) { order = append(order, "c") })
	bus.Emit("x", nil)
	order = append(order, "|")
	bus.Emit("x", nil)
	if got := strings.Join(order, ","); got != "a,b,c,|,a,c" {
		t.Fatalf("order = %s", got)
	}
}
