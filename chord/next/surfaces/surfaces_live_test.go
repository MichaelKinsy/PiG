package surfaces

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/internal/deskfix"
)

// pi: packages/chord/test/surfaces/live.test.ts (slice 15 §1.5): a reference read while live: its granted set while
// live, and while it loads again after it last settled live (a rebind, a link drop), nothing otherwise (no reference,
// its first load, failed or denied, ended).

var untilState = deskfix.UntilState

func kindOf(ref *host.Reference) host.RefStateKind { return ref.State().Value().Kind }

func roleOf(granted map[string]bool) string {
	if granted == nil {
		return "undefined"
	}
	if granted["write"] {
		return "editor"
	}
	return "reader"
}

// pi: packages/chord/src/surfaces/live.ts
func TestLiveGrant(t *testing.T) {
	t.Run("is the granted set while live, nothing before first bind or for no reference", func(t *testing.T) {
		desk := deskfix.New(t)
		open := desk.Gate()
		held, err := desk.Client.Hold(bg, deskfix.Desk, delta.JsonObjectOf(), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if LiveGrant(held) != nil {
			t.Fatal("LiveGrant before it binds is not nil")
		}
		if LiveGrant(nil) != nil {
			t.Fatal("LiveGrant of no reference is not nil")
		}
		open()
		untilState(t, held, func() bool { return kindOf(held) == host.RefStateLive })
		got := LiveGrant(held)
		if got == nil || got["write"] {
			t.Fatalf("LiveGrant(held) = %v, want non-nil without write", got)
		}
		held.Release()
	})

	t.Run("stays live across rebind loading, clears on denial, nothing while loading after denial or released", func(t *testing.T) {
		desk := deskfix.New(t)
		held, err := desk.Client.Hold(bg, deskfix.Desk, delta.JsonObjectOf(), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		untilState(t, held, func() bool { return kindOf(held) == host.RefStateLive })
		var mu sync.Mutex
		var seen []string
		arrived := make(chan struct{}, 1)
		watch, err := held.State().Watch(bg)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = watch.Stop() }()
		watch.Start(func(_ context.Context, call chord.NativeWatchCall[*host.RefState]) error {
			if call.Kind != chord.WatchCallValue {
				return nil
			}
			read := LiveGrant(held)
			mu.Lock()
			seen = append(seen, string(call.Value.Kind)+": "+roleOf(read))
			mu.Unlock()
			select {
			case arrived <- struct{}{}:
			default:
			}
			return nil
		})
		delivered := func(n int) {
			t.Helper()
			for {
				mu.Lock()
				count := len(seen)
				mu.Unlock()
				if count >= n {
					return
				}
				select {
				case <-arrived:
				case <-time.After(30 * time.Second):
					t.Fatalf("state %d never delivered", n)
				}
			}
		}
		admit := func(mode string) {
			t.Helper()
			if err := desk.Admit(mode); err != nil {
				t.Fatal(err)
			}
		}
		open := desk.Gate()
		admit("editor")
		delivered(1)
		open()
		untilState(t, held, func() bool { return kindOf(held) == host.RefStateLive && deskfix.HasGrant(held, "write") })
		delivered(2)
		if _, err := held.Call("write", "edited", bg); err != nil {
			t.Fatal(err)
		}
		admit("none")
		untilState(t, held, func() bool {
			state := held.State().Value()
			return state.Kind == host.RefStateFailed && state.Cause.Kind == data.FailureDenied
		})
		delivered(3)
		if LiveGrant(held) != nil {
			t.Fatal("LiveGrant(held) once denied is not nil")
		}
		open = desk.Gate()
		admit("reader")
		delivered(4)
		open()
		untilState(t, held, func() bool { return kindOf(held) == host.RefStateLive && !deskfix.HasGrant(held, "write") })
		delivered(5)
		// While loading after live (rebind), LiveGrant keeps the last granted set; after denial, loading returns nil.
		want := []string{
			"loading: reader",
			"live: editor",
			"failed: undefined",
			"loading: undefined",
			"live: reader",
		}
		mu.Lock()
		got := append([]string(nil), seen...)
		mu.Unlock()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seen = %q, want %q", got, want)
		}
		held.Release()
		if LiveGrant(held) != nil {
			t.Fatal("LiveGrant of a released reference is not nil")
		}
	})
}
