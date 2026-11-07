package surfaces

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"time"
	"weak"

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
	if _, err := h.Load(nil, context.Background()); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPageStateKeepsAKeysValueForWhoeverAsksNext(t *testing.T) {
	h := client(t)
	defer h.Close(context.Background())
	PageStateOf(h, "draft", "").Set("oat mi")
	if got := PageStateOf(h, "draft", "ignored").Read(); got != "oat mi" {
		t.Errorf("draft = %q", got)
	}
}

func TestPageStateStartsANewKeyOrTheSameKeyOnAnotherHostAtItsInitialValue(t *testing.T) {
	h, other := client(t), client(t)
	defer h.Close(context.Background())
	defer other.Close(context.Background())
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
	defer h.Close(context.Background())
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
	defer h.Close(context.Background())
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
	hostRef, valueRef := func() (weak.Pointer[int], weak.Pointer[kept]) {
		created := client(t)
		value := &kept{"oat mi"}
		PageStateOf(created, "draft", value)
		created.Close(context.Background())
		// The weak pointer to the host is taken through its address: the host is the one pointer CreateHost returned.
		return weak.Make((*int)(reflect.ValueOf(created).UnsafePointer())), weak.Make(value)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for (hostRef.Value() != nil || valueRef.Value() != nil) && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if hostRef.Value() != nil || valueRef.Value() != nil {
		t.Errorf("host collected: %v, value collected: %v", hostRef.Value() == nil, valueRef.Value() == nil)
	}
}
