package host

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/host/route.test.ts › routes › resolve's `opened` › is told the peer's incarnation at the
// handshake, before the peer's first call is handled; never for a link closed before it (Pi main f57cd9484).
//
// Go forms: Pi's resolve makes the worker itself; here the relay's resolve (`made()`) does, and the worker's first call is a task
// of the worker's loop (`void worker.get(...).then(...)`) that ends with the answer, which the test receives from `answered`.
func TestRouteResolvesOpenedIsToldThePeersIncarnationAtTheHandshakeBeforeThePeersFirstCallIsHandledNeverForALinkClosedBeforeIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		who := exRemote("app.who", contracts.Params{}, exServerAddress, contracts.Var("v", contracts.VariantDeclaration{
			Methods: []contracts.MethodEntry{contracts.Meth("incarnation", contracts.NewMethod(contracts.Schema{}, exStr()))},
		}))
		var mu sync.Mutex
		var events []string
		record := func(event string) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		}
		recorded := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), events...)
		}
		answered := make(chan struct{})
		var r *relayed
		r = relay(t, relayedOptions{
			Server: func(h FacetHost) error {
				return h.Serve(who, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
					return exVariant("v", nil, map[string]contracts.MethodFunction{
						"incarnation": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
							record("handled " + exCtx(ctx).Incarnation())
							return exCtx(ctx).Incarnation(), nil
						},
					}), nil
				}, ServeOptions{})
			},
			Resolve: func(address contracts.Address, made func() resolved, _ context.Context) (resolved, error) {
				opened := func(incarnation string) { record("opened " + incarnation) }
				if address.Params.Value("id") == "dead" {
					a, b := link.MemoryLinkPair()
					b.Close()
					return resolved{Link: a, Principal: workerPrincipal("dead"), Opened: opened}, nil
				}
				worker := made()
				worker.Opened = opened
				r.WorkerLoaded(t, address.Key)
				host := r.Worker(address.Key)
				// Its first call, made before the server has the link: it goes out with the handshake.
				startTask(host.(*chordHost).loop(), func() {
					defer close(answered)
					reference, err := host.Get(background, who, jsonObject(), GetOptions{})
					if err != nil {
						record("get failed: " + err.Error())
						return
					}
					result, err := reference.Call("incarnation", nil, background)
					if err != nil {
						record("call failed: " + err.Error())
						return
					}
					record(fmt.Sprintf("answered %v", result))
				})
				return worker, nil
			},
		})
		_, err := r.Server.Hold(background, PlacedNotes, routeS("S1"), GetOptions{})
		must(t, err)
		<-answered
		r.Transported()
		r.Transported()
		incarnation := r.Worker("session|S1").Incarnation()
		if want := []string{"opened " + incarnation, "handled " + incarnation, "answered " + incarnation}; !reflect.DeepEqual(recorded(), want) {
			t.Fatalf("events = %v, want %v", recorded(), want)
		}
		_, err = r.Server.Hold(background, PlacedNotes, routeS("dead"), GetOptions{})
		must(t, err)
		r.Transported()
		if got := len(recorded()); got != 3 {
			t.Fatalf("events = %v, want no event for a link closed before its handshake", recorded())
		}
	})
}

// lateLink is a link whose listener is set on a goroutine of its own: what arrives on it is handled after the section that made the link
// ended, with the link registered on its host (a peer's `hello` that arrived first is handled while the link is made, before that).
type lateLink struct{ link.Link }

func (l lateLink) OnMessage(listener func(message []byte)) { go l.Link.OnMessage(listener) }

// Go-only; the order of Pi's host-link.ts `opened()` at f57cd9484: `this.#opened?.(this.#channel.peer)` first, then `consumer.opened(...)`,
// then `host.linkOpened`. "First: whoever asked knows the peer before anything else it sent is handled, and before this host sends": the
// route's `opened` is told before this host's own side of the link is opened, so before the calls that waited for the peer's `hello`
// go out. Read in the section that opens the link, where the consumer side learns the peer's call window right after `opened`.
func TestRouteResolvesOpenedIsToldBeforeThisHostsOwnSideOfTheLinkIsOpened(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var mu sync.Mutex
		var events []string
		told := make(chan struct{})
		var r *relayed
		r = relay(t, relayedOptions{
			Resolve: func(_ contracts.Address, made func() resolved, _ context.Context) (resolved, error) {
				worker := made()
				worker.Link = lateLink{worker.Link}
				worker.Opened = func(string) {
					defer close(told)
					mu.Lock()
					defer mu.Unlock()
					for _, l := range r.Server.(*chordHost).links {
						if l.role == linkRoleRoute {
							events = append(events, fmt.Sprintf("opened, the peer's call window known to this host: %t", l.consumer.greeted != nil))
						}
					}
				}
				return worker, nil
			},
		})
		_, err := r.Server.Hold(background, PlacedNotes, routeS("S1"), GetOptions{})
		must(t, err)
		// The worker's `hello` goes out once its host loaded; its arrival is what `opened` is told at.
		r.WorkerLoaded(t, "session|S1")
		select {
		case <-told:
		case <-time.After(10 * time.Second):
			t.Fatal("never happened: the route's opened was told")
		}
		mu.Lock()
		defer mu.Unlock()
		if want := []string{"opened, the peer's call window known to this host: false"}; !reflect.DeepEqual(events, want) {
			t.Fatalf("events = %v, want %v", events, want)
		}
	})
}
