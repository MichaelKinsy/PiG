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

// pi: packages/chord/test/surfaces/variant.test.ts (Pi f57cd9484): a reference read in one variant (decision "C15 stands; the
// UI bindings follow the variant"): narrowed while live in that variant, nothing before it binds, in another variant, while it
// loads between two, denied or released.

var (
	untilState  = deskfix.UntilState
	variantName = deskfix.VariantName
)

func kindOf(ref *host.Reference) host.RefStateKind { return ref.State().Value().Kind }

func TestLiveIn(t *testing.T) {
	t.Run("is the reference while it is live in the variant, nothing before it binds or for no reference", func(t *testing.T) {
		desk := deskfix.New(t)
		// Hold the desk loading until the before-bind read has run: Pi holds and reads in one turn,
		// while Go's memory link delivers on a goroutine and may bind before LiveIn runs.
		open := desk.Gate()
		held, err := desk.Client.Hold(bg, deskfix.Desk, delta.JsonObjectOf(), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if LiveIn(held, "reader") != nil {
			t.Fatal("LiveIn before it binds is not nil")
		}
		if LiveIn(nil, "reader") != nil {
			t.Fatal("LiveIn of no reference is not nil")
		}
		open()
		untilState(t, held, func() bool { return kindOf(held) == host.RefStateLive })
		if LiveIn(held, "reader") != held {
			t.Fatal("LiveIn(held, reader) is not held")
		}
		if LiveIn(held, "editor") != nil {
			t.Fatal("LiveIn(held, editor) is not nil")
		}
		held.Release()
	})

	t.Run("follows the variant an admission rebinds: nothing while it loads between two, denied or released", func(t *testing.T) {
		desk := deskfix.New(t)
		held, err := desk.Client.Hold(bg, deskfix.Desk, delta.JsonObjectOf(), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		untilState(t, held, func() bool { return LiveIn(held, "reader") == held })
		// Each state as its watch gets it, with what the reference is read as then.
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
			read := LiveIn(held, "reader")
			if read == nil {
				read = LiveIn(held, "editor")
			}
			readAs := "undefined"
			if read != nil {
				readAs = variantName(read)
			}
			mu.Lock()
			seen = append(seen, string(call.Value.Kind)+" "+variantName(held)+": read as "+readAs)
			mu.Unlock()
			select {
			case arrived <- struct{}{}:
			default:
			}
			return nil
		})
		// Go's watch delivers on a goroutine, by when the reference may have moved on: each step waits for the delivery of the
		// state it reads, and an admission that rebuilds the object holds it loading (the fixture's gate) until its loading is read.
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
		untilState(t, held, func() bool { return LiveIn(held, "editor") == held })
		delivered(2)
		if LiveIn(held, "reader") != nil {
			t.Fatal("LiveIn(held, reader) in the editor variant is not nil")
		}
		if _, err := LiveIn(held, "editor").Call("write", "edited", bg); err != nil {
			t.Fatal(err)
		}
		admit("none")
		untilState(t, held, func() bool {
			state := held.State().Value()
			return state.Kind == host.RefStateFailed && state.Cause.Kind == data.FailureDenied
		})
		delivered(3)
		if LiveIn(held, "editor") != nil {
			t.Fatal("LiveIn(held, editor) once denied is not nil")
		}
		open = desk.Gate()
		admit("reader")
		delivered(4)
		open()
		untilState(t, held, func() bool { return LiveIn(held, "reader") == held })
		delivered(5)
		// While it loads between two variants it still says the last one: the comparison alone would keep an editor's
		// controls up.
		want := []string{
			"loading reader: read as undefined",
			"live editor: read as editor",
			"failed undefined: read as undefined",
			"loading undefined: read as undefined",
			"live reader: read as reader",
		}
		mu.Lock()
		got := append([]string(nil), seen...)
		mu.Unlock()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seen = %q, want %q", got, want)
		}
		held.Release()
		if LiveIn(held, "reader") != nil {
			t.Fatal("LiveIn of a released reference is not nil")
		}
	})

	t.Run("narrows a held and a got reference to the variant's members", func(t *testing.T) {
		// Pi's type-level case (`InVariant`, `VariantOf`) has no Go counterpart: a reference is one type, and the variant's
		// members are what its calls reach. What the narrowing promises at run time: a reference narrowed to `editor` has
		// `write`, one narrowed to `reader` has not, and a variant the type does not declare narrows nothing (Pi's
		// `@ts-expect-error` refuses it at compile time).
		desk := deskfix.New(t)
		held, err := desk.Client.Hold(bg, deskfix.Desk, delta.JsonObjectOf(), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer held.Release()
		untilState(t, held, func() bool { return LiveIn(held, "reader") == held })
		if variant := variantName(LiveIn(held, "reader")); variant != "reader" {
			t.Fatalf("variant = %q, want reader", variant)
		}
		if _, err := LiveIn(held, "reader").Call("write", "x", bg); err == nil {
			t.Fatal("a reader has write")
		}
		got, err := desk.Client.Get(bg, deskfix.Desk, delta.JsonObjectOf(), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer got.Release()
		if LiveIn(got, "reader") != got || LiveIn(got, "editor") != nil || LiveIn(got, "owner") != nil {
			t.Fatal("a got reference does not narrow to its variant alone")
		}
		if err := desk.Admit("editor"); err != nil {
			t.Fatal(err)
		}
		untilState(t, held, func() bool { return LiveIn(held, "editor") == held })
		if variant := variantName(LiveIn(held, "editor")); variant != "editor" {
			t.Fatalf("variant = %q, want editor", variant)
		}
		if _, err := LiveIn(held, "editor").Call("write", "x", bg); err != nil {
			t.Fatal(err)
		}
	})
}
