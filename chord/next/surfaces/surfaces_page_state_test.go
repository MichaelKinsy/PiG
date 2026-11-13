package surfaces

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// pi: packages/chord/test/surfaces/page-state.test.ts

// client is a client host with nothing to load.
func client(t testing.TB) host.Host {
	t.Helper()
	h, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closed(t, h.Close(context.Background())) })
	if _, err := h.Load(nil, context.Background()); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPageStateKeepsAKeysValueForWhoeverAsksNext(t *testing.T) {
	h := client(t)
	defer func() { closed(t, h.Close(context.Background())) }()
	PageStateOf(h, "draft", "").Set("oat mi")
	if got := PageStateOf(h, "draft", "ignored").Read(); got != "oat mi" {
		t.Errorf("draft = %q", got)
	}
}

func TestPageStateStartsANewKeyOrTheSameKeyOnAnotherHostAtItsInitialValue(t *testing.T) {
	h, other := client(t), client(t)
	defer func() { closed(t, h.Close(context.Background())) }()
	defer func() { closed(t, other.Close(context.Background())) }()
	PageStateOf(h, "draft", "").Set("oat mi")
	if got := PageStateOf(h, "scroll", 0).Read(); got != 0 {
		t.Errorf("scroll = %d", got)
	}
	if got := PageStateOf(other, "draft", "").Read(); got != "" {
		t.Errorf("other draft = %q", got)
	}
}

func TestPageStateHoldsAnyValueAsItWasSetNeverCopied(t *testing.T) {
	h := client(t)
	defer func() { closed(t, h.Close(context.Background())) }()
	type selection struct {
		start, end int
		element    func() any
	}
	chosen := &selection{2, 4, func() any { return nil }}
	PageStateOf[*selection](h, "selection", nil).Set(chosen)
	if got := PageStateOf[*selection](h, "selection", nil).Read(); got != chosen {
		t.Errorf("selection = %p, want %p", got, chosen)
	}
}

func TestPageStateCallsEverySubscriberAfterASetThatChangedTheValueUntilItUnsubscribes(t *testing.T) {
	h := client(t)
	defer func() { closed(t, h.Close(context.Background())) }()
	state := PageStateOf(h, "draft", "")
	var calls []string
	first := state.Subscribe(func() { calls = append(calls, "first "+state.Read()) })
	state.Subscribe(func() { calls = append(calls, "second "+state.Read()) })
	state.Set("a")
	state.Set("a")
	first()
	state.Set("b")
	if want := []string{"first a", "second a", "second b"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

func TestPageStateIsCollectedWithItsHost(t *testing.T) {
	type kept struct{ text string }
	hostGone, valueGone := make(chan struct{}), make(chan struct{})
	func() {
		// Not client(t): its cleanup would keep the host reachable until the test ends.
		created, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := created.Load(nil, context.Background()); err != nil {
			t.Fatal(err)
		}
		value := &kept{"oat mi"}
		PageStateOf(created, "draft", value)
		closed(t, created.Close(context.Background()))
		// The host is the one pointer CreateHost returned.
		runtime.AddCleanup((*byte)(reflect.ValueOf(created).UnsafePointer()), func(struct{}) { close(hostGone) }, struct{}{})
		runtime.AddCleanup(value, func(struct{}) { close(valueGone) }, struct{}{})
	}()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	for _, gone := range []chan struct{}{hostGone, valueGone} {
		for collected := false; !collected; {
			runtime.GC()
			select {
			case <-gone:
				collected = true
			case <-timeout.C:
				t.Fatal("the host or its page state was not collected")
			default:
				runtime.Gosched()
			}
		}
	}
}

func TestPageStateOfAnInterfaceReadsNilAndAStructHoldingASliceIsNeverTheSame(t *testing.T) {
	h := client(t)
	nothing := PageStateOf[any](h, "nothing", nil)
	if got := nothing.Read(); got != nil {
		t.Fatalf("Read = %v, want nil", got)
	}
	type holder struct{ list any }
	state := PageStateOf(h, "holder", holder{list: []int{1}})
	notified := 0
	state.Subscribe(func() { notified++ })
	state.Set(holder{list: []int{1}})
	if notified != 1 {
		t.Fatalf("notified %d times, want 1 (a struct holding a slice is never the same)", notified)
	}
}
