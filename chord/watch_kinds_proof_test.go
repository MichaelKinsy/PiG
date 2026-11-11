package chord

import "testing"

// Proof of watch.ts's tagged unions: a WatchEnd and a WatchCall carry the exact tags Pi's `kind` discriminates on
// (`stopped`, `aborted`, `ended`, `listener_error`, `failed`; `value`, `failed`), which a plugin matches as strings.

func TestWatchEndAndCallKindsAreTheTagsPiDiscriminatesOn(t *testing.T) {
	for got, want := range map[WatchEndKind]string{
		WatchStopped: "stopped", WatchAborted: "aborted", WatchEnded: "ended", WatchListenerError: "listener_error", WatchFailed: "failed",
	} {
		if string(got) != want {
			t.Fatalf("watch end kind %q, want %q", got, want)
		}
	}
	for got, want := range map[WatchCallKind]string{WatchCallValue: "value", WatchCallFailed: "failed"} {
		if string(got) != want {
			t.Fatalf("watch call kind %q, want %q", got, want)
		}
	}
	var end Watchable[string]
	if end != nil {
		t.Fatal("a Watchable zero value is not nil")
	}
}
