package host

// pi: packages/chord/test/examples/11-linger-and-retry.ts

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// --- 11-linger-and-retry.ts ----------------------------------------------------------------------------------------

func TestExample11LingerAndRetry(t *testing.T) {
	draft := exLocal("example.draft", contracts.Params{exProp("conversation", exStr())},
		contracts.Mem("text", exStr()), contracts.Mem("type", contracts.NewMethod(exObj(exProp("text", exStr())))))
	mirror := exLocal("example.mirror", contracts.Params{exProp("id", exInt())}, contracts.Mem("status", exStr()))
	draftDoc := exDefineState("draft", documents.StateOptions{
		Schema:  exObj(exProp("text", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("text", "") },
	})
	var (
		mu         sync.Mutex
		configured bool
		up         bool
		activated  int
	)
	flag := func(f *bool) bool { mu.Lock(); defer mu.Unlock(); return *f }
	setFlag := func(f *bool, v bool) { mu.Lock(); defer mu.Unlock(); *f = v }
	linger := 200 * time.Millisecond
	errs := &hostErrors{}
	var reported exCollector
	host := newHost(t, HostOptions{OnError: func(err HostError) { errs.push(err); reported.add(string(err.Kind)) }})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@example/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		// 1. A draft lingers 200 ms after its last reference: a tab that closes and reopens quickly finds its text.
		if err := h.Serve(draft, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			activated++
			mu.Unlock()
			doc := stateOf(self, draftDoc)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"text": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("text"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"type": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(d *delta.Object) { _ = d.Set("text", args.Value("text")) }, ctx)
				}),
			}), nil
		}, ServeOptions{Linger: &linger}); err != nil {
			return err
		}
		return h.Serve(mirror, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) {
				// `broken`: retrying cannot help until someone changes something (no automatic retry).
				if !flag(&configured) {
					return nil, data.Broken(errors.New("no mirror configured"))
				}
				// A plain throw is transient: retried with backoff (1 s, doubling).
				if !flag(&up) {
					return nil, errors.New("mirror unreachable")
				}
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"status": exConstant("mirroring")}, nil), nil
		}, ServeOptions{})
	})}}, background))
	draftRef := exGet(t, host, draft, jsonObject("conversation", "c1"))
	exCall(t, draftRef, "type", jsonObject("text", "hello"))
	draftRef.Release()
	// Within the linger the object is reused with its text.
	draftRef = exGet(t, host, draft, jsonObject("conversation", "c1"))
	exEqual(t, "within linger", exValue(t, draftRef, "text"), "hello")
	mu.Lock()
	if activated != 1 {
		t.Fatalf("activations within the linger = %d", activated)
	}
	mu.Unlock()
	draftRef.Release()
	scopeEventually(t, "the draft lingers", func() bool { return exActivity(t, host).Lingering == 1 })
	scopeEventually(t, "the linger ended", func() bool { return exActivity(t, host).Lingering == 0 })
	draftRef = exGet(t, host, draft, jsonObject("conversation", "c1"))
	exEqual(t, "after linger", exValue(t, draftRef, "text"), "")
	draftRef.Release()
	// 2. The mirror: broken, then transient, then live.
	// Pi's `host.hold(...)` and `ref.state.watch(...)` run in one synchronous section: the mirror's open fails in a later turn,
	// so the watch opens at `loading` and its first frame is the broken failure. Taken after the hold's section, the watch
	// could open at the failure already, and its first frame would be the retry's.
	var (
		held  *Reference
		watch chord.NativeWatchHandle[*RefState]
		kinds *exCollector
	)
	host.(*chordHost).enter(func() {
		held = exHold(t, host, mirror, jsonObject("id", float64(1)))
		watch, kinds = exStateKinds(t, held)
	})
	want := []string{"failed, broken, no automatic retry: example.mirror(1): no mirror configured"}
	exWaitState(t, held, want[0])
	setFlag(&configured, true)
	held.Retry()
	scopeEventually(t, "the mirror failed transient", func() bool {
		current := held.State().Value()
		return current.Kind == RefStateFailed && current.RetryAt != nil && strings.HasSuffix(current.Cause.Error.Message, "mirror unreachable")
	})
	setFlag(&up, true)
	held.Retry()
	scopeEventually(t, "the mirror is live", func() bool { return exStateNow(t, held) == "live" })
	exEqual(t, "status", exValue(t, held, "status"), "mirroring")
	// Pi's `await wait(10)` lets the `live` frame reach the state watch before the reference is released.
	deliveredOn(host)
	held.Retry()
	held.Release()
	<-watch.Closed()
	got := kinds.all()
	wantKinds := []string{
		"failed, broken, no automatic retry: example.mirror(1): no mirror configured", "loading", "failed, transient, retries: example.mirror(1): mirror unreachable", "loading", "live",
	}
	if len(got) < len(wantKinds) || !reflect.DeepEqual(got[:len(wantKinds)], wantKinds) {
		t.Fatalf("ref.state frames = %v, want prefix %v", got, wantKinds)
	}
	exCloseHost(t, host)
	// The mirror's failures are reported as activation_failed; nothing else.
	for _, kind := range reported.all() {
		if kind != string(ErrActivationFailed) {
			t.Fatalf("reported %v", reported.all())
		}
	}
}
