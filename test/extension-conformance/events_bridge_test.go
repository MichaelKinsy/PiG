package extensionconformance

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Upstream's pi.events is one EventEmitter per session (packages/coding-agent/src/core/event-bus.ts:12-33): listeners run in registration order, each one's synchronous prefix finishes before emit returns, a listener's error is caught and printed as `Event handler error (<channel>):` (event-bus.ts:19-25), and the unsubscribe function removes only that listener (event-bus.ts:27). The native SDKs bridge that bus by value. The node realm is the control: it runs upstream's own semantics, and a native realm must produce the same observations wherever a payload by value can.

// busNativeLanguages lists the SDKs that bridge pi.events.
var busNativeLanguages = []string{"go", "python", "rust"}

var busIsolations = []string{"isolated", "shared-ok"}

// eachBusRealm runs body for the node control and for every native language, under both topologies. Each row owns its Host, processes and temp directories, so the rows run in parallel with each other and with other parallel tests. A body that captures the process's stderr uses eachBusRealmSerial.
func eachBusRealm(t *testing.T, controlToo bool, body func(t *testing.T, language, isolation string)) {
	t.Helper()
	t.Parallel()
	eachBusRealmRows(t, controlToo, true, body)
}

// eachBusRealmSerial is eachBusRealm for a body that swaps the process's stderr (captureStderr): it must run alone, so its rows are not parallel.
func eachBusRealmSerial(t *testing.T, controlToo bool, body func(t *testing.T, language, isolation string)) {
	t.Helper()
	eachBusRealmRows(t, controlToo, false, body)
}

func eachBusRealmRows(t *testing.T, controlToo, parallel bool, body func(t *testing.T, language, isolation string)) {
	t.Helper()
	languages := busNativeLanguages
	if controlToo {
		languages = append([]string{"node"}, languages...)
	}
	for _, language := range languages {
		for _, isolation := range busIsolations {
			t.Run(language+"/"+isolation, func(t *testing.T) {
				if parallel {
					t.Parallel()
				}
				body(t, language, isolation)
			})
		}
	}
}

// captureStderr returns a function that stops capturing and returns what the Host reported.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&out, reader)
		close(done)
	}()
	stopped := false
	stop := func() string {
		if !stopped {
			stopped = true
			os.Stderr = original
			_ = writer.Close()
			<-done
			_ = reader.Close()
		}
		return out.String()
	}
	t.Cleanup(func() { stop() })
	return stop
}

func countHandlerErrors(stderr, channel string) int {
	return strings.Count(stderr, "Event handler error ("+channel+"): ")
}

func busSpec(language, isolation, name string, listeners ...busListenerAt) busFixture {
	return busFixture{Name: name, language: language, isolation: isolation, Listeners: listeners}
}

// A node emitter reaches a native listener, and the native listener's own emit reaches a node observer. The observer's line carries the value the listener computed from the payload, so no fallback can produce it (event-bus.ts:15-17).
func TestNativeEventBusNodeEmitterReachesListenerAndItsEcho(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec("node", isolation, "emitter"),
			busSpec(language, isolation, "listener", busListenerAt{"in", "record+echo=out"}),
			busSpec("node", isolation, "observer", busListenerAt{"out", "record"}),
		)
		rig.must("emitter", "emit", `in json {"a":1,"b":[1,2],"c":{"d":"é"}}`)
		rig.expect(
			`listener|in|{"a":1,"b":[1,2],"c":{"d":"é"}}`,
			`observer|out|{"by":"listener","got":{"a":1,"b":[1,2],"c":{"d":"é"}}}`,
		)
	})
}

// A payload crosses by value as the emitter's JSON.stringify view, computed in the emitter's realm when the listener runs: toJSON runs there, and a single listener reads each getter once. A payload with no JSON form (undefined) is null.
func TestNativeEventBusNodePayloadIsTheEmittersJSONView(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec("node", isolation, "emitter"),
			busSpec(language, isolation, "listener", busListenerAt{"view", "record"}),
		)
		rig.must("emitter", "emit", "view toJSON")
		rig.must("emitter", "emit", "view getter")
		rig.must("emitter", "emit", "view getter")
		rig.must("emitter", "emit", "view undefined")
		rig.expect(
			`listener|view|{"via":"toJSON"}`,
			`listener|view|{"n":1}`,
			`listener|view|{"n":2}`,
			`listener|view|null`,
		)
	})
}

// EventEmitter passes one payload object to every listener in turn (event-bus.ts:15-17), so a listener observes what the listeners before it wrote. A native listener's by-value view is the emitter's object as it is when that listener runs, not a view cached by an earlier native listener of the same emit.
func TestNativeEventBusNativeViewSeesEarlierNodeListenersWrites(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec("node", isolation, "emitter"),
			busSpec("node", isolation, "n1", busListenerAt{"c", "inc"}),
			busSpec(language, isolation, "g1", busListenerAt{"c", "record"}),
			busSpec("node", isolation, "n2", busListenerAt{"c", "inc"}),
			busSpec(language, "isolated", "g2", busListenerAt{"c", "record"}),
		)
		rig.must("emitter", "emit", `c json {"n":0}`)
		rig.expect(`g1|c|{"n":1}`, `g2|c|{"n":2}`)
	})
}

// A payload without a JSON form (BigInt, a cycle) fails only the native listener's delivery. The emitter and the node listener are unaffected, and the failure is reported as upstream's safeHandler reports a listener error (event-bus.ts:19-25).
func TestNativeEventBusUnserializableNodePayloadReachesNoNativeListener(t *testing.T) {
	eachBusRealmSerial(t, false, func(t *testing.T, language, isolation string) {
		for _, kind := range []string{"bigint", "bigintfield", "cycle"} {
			t.Run(kind, func(t *testing.T) {
				stop := captureStderr(t)
				rig := newBusRig(t,
					busSpec("node", isolation, "emitter"),
					busSpec(language, isolation, "native", busListenerAt{"c", "record"}),
					busSpec("node", isolation, "observer", busListenerAt{"c", "record"}),
				)
				rig.must("emitter", "emit", "c "+kind)
				rig.expect(`observer|c|"<unserializable>"`)
				rig.must("emitter", "emit", "c json 5")
				rig.expect(`observer|c|"<unserializable>"`, `native|c|5`, `observer|c|5`)
				if got := countHandlerErrors(stop(), "c"); got != 1 {
					t.Fatalf("Event handler error (c) reported %d times, want 1", got)
				}
			})
		}
	})
}

// A native emitter reaches a node listener and a listener in another cell, in registration order, and each receives its own decoded copy.
func TestNativeEventBusNativeEmitterReachesNodeAndOtherCellListeners(t *testing.T) {
	eachBusRealm(t, false, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec(language, isolation, "emitter"),
			busSpec("node", isolation, "n1"),
			busSpec(language, "isolated", "other"),
			busSpec("node", isolation, "n2"),
			busSpec(language, isolation, "sibling"),
		)
		rig.must("n1", "sub", "ch inc+record")
		rig.must("other", "sub", "ch inc+record")
		rig.must("n2", "sub", "ch record")
		rig.must("sibling", "sub", "ch record")
		rig.must("emitter", "emit", `ch json {"k":[1,2,{"z":null}],"n":0,"s":"é"}`)
		rig.expect(
			`n1|ch|{"k":[1,2,{"z":null}],"n":1,"s":"é"}`,
			`other|ch|{"k":[1,2,{"z":null}],"n":1,"s":"é"}`,
			`n2|ch|{"k":[1,2,{"z":null}],"n":0,"s":"é"}`,
			`sibling|ch|{"k":[1,2,{"z":null}],"n":0,"s":"é"}`,
		)
	})
}

// Listeners run in registration order across realms, an unsubscribe removes only its own listener, unsubscribe is idempotent, and a new subscription goes to the end (event-bus.ts:12-33, EventEmitter).
func TestNativeEventBusRegistrationOrderAcrossRealms(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec("node", isolation, "n1"),
			busSpec(language, isolation, "g"),
			busSpec("node", isolation, "n2"),
			busSpec(language, "isolated", "g2"),
		)
		for _, name := range []string{"n1", "g", "n2", "g2"} {
			rig.must(name, "sub", "order record")
		}
		rig.must("n1", "emit", "order json 1")
		rig.expect(`n1|order|1`, `g|order|1`, `n2|order|1`, `g2|order|1`)
		rig.reset()
		rig.must("g", "unsub", "0")
		rig.must("g", "unsub", "0")
		rig.must("n1", "emit", "order json 2")
		rig.expect(`n1|order|2`, `n2|order|2`, `g2|order|2`)
		rig.reset()
		rig.must("g", "sub", "order record")
		rig.must("n1", "emit", "order json 3")
		rig.expect(`n1|order|3`, `n2|order|3`, `g2|order|3`, `g|order|3`)
	})
}

// A listener that emits from inside its handler runs the nested dispatch before the outer emit returns (event-bus.ts:15-17, EventEmitter is synchronous), across every realm kind.
func TestNativeEventBusReentrantEmitFromHandler(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec("node", isolation, "n"),
			busSpec(language, isolation, "g1", busListenerAt{"a", "record+echo=b"}),
			busSpec("node", isolation, "n2", busListenerAt{"b", "record+echo=c"}),
			busSpec(language, "isolated", "g2", busListenerAt{"c", "record"}),
		)
		rig.must("n", "emit", `a json {"v":1}`)
		rig.expect(
			`g1|a|{"v":1}`,
			`n2|b|{"by":"g1","got":{"v":1}}`,
			`g2|c|{"by":"n2","got":{"by":"g1","got":{"v":1}}}`,
		)
	})
}

// Two realms emit into each other at once. Each services the other's dispatch while it waits in its own emit, so neither deadlocks and every payload arrives. TestXrefEventBusCrossingEmittersDoNotDeadlock is the node baseline.
func TestNativeEventBusCrossingEmittersDoNotDeadlock(t *testing.T) {
	eachBusRealm(t, false, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec(language, isolation, "g", busListenerAt{"to-g", "record"}),
			busSpec("node", isolation, "n", busListenerAt{"to-n", "record"}),
		)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Go(func() { errs <- rig.run("g", "burst", "to-n 100") })
		wg.Go(func() { errs <- rig.run("n", "burst", "to-g 100") })
		finished := make(chan struct{})
		go func() { wg.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(60 * time.Second):
			t.Fatal("crossing emitters deadlocked")
		}
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var toG, toN int
		for _, line := range rig.lines() {
			switch {
			case strings.HasPrefix(line, "g|to-g|"):
				toG++
			case strings.HasPrefix(line, "n|to-n|"):
				toN++
			}
		}
		if toG != 100 || toN != 100 {
			t.Fatalf("delivered to-g=%d to-n=%d, want 100 each", toG, toN)
		}
	})
}

// A listener's error or panic is caught and reported without reaching the emitter or the listeners after it (event-bus.ts:19-25).
func TestNativeEventBusListenerFailureDoesNotReachEmitterOrLaterListeners(t *testing.T) {
	eachBusRealmSerial(t, true, func(t *testing.T, language, isolation string) {
		emitters := []string{"node"}
		if language != "node" {
			emitters = append(emitters, language)
		}
		for _, emitter := range emitters {
			t.Run("emitter-"+emitter, func(t *testing.T) {
				stop := captureStderr(t)
				rig := newBusRig(t,
					busSpec(emitter, isolation, "e"),
					busSpec(language, isolation, "failing", busListenerAt{"ch", "fail"}, busListenerAt{"ch", "panic"}),
					busSpec(language, "isolated", "later", busListenerAt{"ch", "record"}),
				)
				rig.must("e", "emit", `ch json {"ok":true}`)
				rig.expect(`later|ch|{"ok":true}`)
				rig.must("e", "emit", `ch json {"ok":2}`)
				rig.expect(`later|ch|{"ok":true}`, `later|ch|{"ok":2}`)
				if got := stop(); language != "node" && countHandlerErrors(got, "ch") != 4 {
					t.Fatalf("Event handler error (ch) reported %d times, want 4 (two failures, two emits)\n%s", countHandlerErrors(got, "ch"), got)
				}
			})
		}
	})
}

// EventEmitter clones its listener array for each emit, so a listener removed during a dispatch is still called for that dispatch and gone from the next.
func TestNativeEventBusUnsubscribeDuringDispatchKeepsTheSnapshot(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec("node", isolation, "e"),
			busSpec(language, isolation, "g"),
		)
		rig.must("g", "sub", "ch unsub=1+record")
		rig.must("g", "sub", "ch record")
		rig.must("e", "emit", "ch json 1")
		rig.expect(`g|ch|1`, `g|ch|1`)
		rig.reset()
		rig.must("e", "emit", "ch json 2")
		rig.expect(`g|ch|2`)
	})
}

// A realm that exits in the middle of a dispatch releases the emitter with the same outcome as a crashed node realm: the emitter returns, the listeners after it run, and the dead realm's listeners are gone from the next emit.
func TestNativeEventBusRuntimeCrashMidDispatchReleasesTheEmitter(t *testing.T) {
	for _, language := range append([]string{"node"}, busNativeLanguages...) {
		t.Run(language, func(t *testing.T) {
			stop := captureStderr(t)
			rig := newBusRig(t,
				busSpec("node", "isolated", "e"),
				busSpec("node", "isolated", "before", busListenerAt{"boom", "record"}),
				busSpec(language, "isolated", "crasher", busListenerAt{"boom", "exit"}, busListenerAt{"other", "record"}),
				busSpec("node", "isolated", "after", busListenerAt{"boom", "record"}),
			)
			rig.must("e", "emit", "boom json 1")
			rig.expect(`before|boom|1`, `after|boom|1`)
			rig.reset()
			rig.must("e", "emit", "boom json 2")
			rig.must("e", "emit", "other json 3")
			rig.expect(`before|boom|2`, `after|boom|2`)
			_ = stop()
		})
	}
}

// A factory that subscribes and then fails has its subscriptions dropped (upstream removes them with the failed runtime); the other realms' listeners are untouched.
func TestNativeEventBusFailedFactorySubscriptionsAreDropped(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		failing := busSpec(language, isolation, "failing", busListenerAt{"ch", "record"})
		failing.FailLoad = true
		rig, failures := newBusRigWithFailures(t,
			busSpec("node", isolation, "e"),
			failing,
			busSpec(language, isolation, "kept", busListenerAt{"ch", "record"}),
		)
		if len(failures) != 1 {
			t.Fatalf("load failures = %v, want the failing factory only", failures)
		}
		rig.must("e", "emit", "ch json 1")
		rig.expect(`kept|ch|1`)
	})
}

// EventEmitter throws for an emit on "error" that has no listener; the emitter observes the failure. With a listener the emit succeeds (event-bus.ts:15-17).
func TestNativeEventBusUnhandledErrorChannel(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec(language, isolation, "e"),
			busSpec("node", isolation, "n"),
		)
		if err := rig.run("e", "emit", `error json {"m":1}`); err == nil {
			t.Fatal("emit on error with no listener returned nil")
		}
		rig.must("n", "sub", "error record")
		rig.must("e", "emit", `error json {"m":2}`)
		rig.expect(`n|error|{"m":2}`)
	})
}

// A native listener declared while the factory runs is registered before the extension is ready, and it is delivered from the first emit.
func TestNativeEventBusFactoryTimeListenerIsLiveAtReady(t *testing.T) {
	eachBusRealm(t, false, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec("node", isolation, "e"),
			busSpec(language, isolation, "g", busListenerAt{"early", "record"}),
		)
		rig.must("e", "emit", "early json 1")
		rig.expect(`g|early|1`)
	})
}

// A native emitter has no microtask queue, so the Host settles the emission before Emit returns: a node listener's first continuation has run when the emitter continues, and the emitter's later work follows it. (A node emitter in upstream's single queue continues first; that ordering has no native counterpart.)
func TestNativeEventBusNativeEmitterSettlesNodeContinuationsBeforeEmitReturns(t *testing.T) {
	eachBusRealm(t, false, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec(language, isolation, "emitter"),
			busSpec("node", isolation, "listener", busListenerAt{"ch", "record+later"}),
		)
		rig.must("emitter", "emitmark", `ch {"v":1}`)
		rig.expect(`listener|ch|{"v":1}`, `listener|ch:later|{"v":1}`, `emitter|ch:returned|null`)
	})
}

// An emitter whose own listener emits again from its handler completes: the nested emit is ordered in the handler's request, not behind the outer emit that is waiting for that handler.
func TestNativeEventBusEmitterReentersThroughItsOwnListener(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec(language, isolation, "g", busListenerAt{"a", "record+echo=b"}, busListenerAt{"b", "record"}),
		)
		done := make(chan error, 1)
		go func() { done <- rig.run("g", "emit", "a json 1") }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("the nested emit waited behind the outer emit")
		}
		rig.expect(`g|a|1`, `g|b|{"by":"g","got":1}`)
	})
}

// A Go extension fused into the Host's own process (a Piglet Binary runs it over an in-memory pipe) joins the same bus: it receives a node emitter's payload and its echo reaches a node observer.
func TestNativeEventBusFusedGoExtensionJoinsTheBus(t *testing.T) {
	t.Parallel()
	rig := newBusRig(t,
		busSpec("node", "isolated", "emitter"),
		busSpec("node", "isolated", "observer", busListenerAt{"out", "record"}),
	)
	fused := sdk.New("fused")
	received := make(chan any, 1)
	if _, err := fused.Events().On("in", func(ctx sdk.Context, data any) error {
		received <- data
		return ctx.Events().Emit("out", map[string]any{"by": "fused", "got": data})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "fused", Enabled: true}, fused.RunWithConn); err != nil {
		t.Fatal(err)
	}
	rig.must("emitter", "emit", `in json {"a":[1,{"b":null}]}`)
	select {
	case data := <-received:
		if got := fmt.Sprint(data); got != "map[a:[1 map[b:<nil>]]]" {
			t.Fatalf("fused listener received %s", got)
		}
	default:
		t.Fatal("the fused listener did not run before the emit returned")
	}
	rig.expect(`observer|out|{"by":"fused","got":{"a":[1,{"b":null}]}}`)
}

// A node emitter's primitive payloads cross as their JSON.stringify text: a string, number, boolean, null and array are themselves, -0 is 0, and values JSON has no text for (NaN, a function, a symbol) are null.
func TestNativeEventBusNodePrimitivePayloads(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec("node", isolation, "emitter"),
			busSpec(language, isolation, "listener", busListenerAt{"p", "record"}),
		)
		for _, payload := range []string{`json "é"`, "json 5", "json null", "json true", `json [1,"a",null]`, "nan", "negzero", "function", "symbol"} {
			rig.must("emitter", "emit", "p "+payload)
		}
		rig.expect(`listener|p|"é"`, `listener|p|5`, `listener|p|null`, `listener|p|true`, `listener|p|[1,"a",null]`, `listener|p|null`, `listener|p|0`, `listener|p|null`, `listener|p|null`)
	})
}

// EventEmitter tells its newListener and removeListener listeners the channel name when a listener is added or removed (event-bus.ts:26-27 call emitter.on and emitter.off), and a native subscription is such a listener.
func TestNativeEventBusNewListenerAndRemoveListenerEvents(t *testing.T) {
	eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
		rig := newBusRig(t,
			busSpec("node", isolation, "watcher", busListenerAt{"newListener", "record"}, busListenerAt{"removeListener", "record"}),
			busSpec(language, isolation, "g"),
		)
		rig.expect(`watcher|newListener|"removeListener"`)
		rig.reset()
		rig.must("g", "sub", "x record")
		rig.must("g", "unsub", "0")
		rig.expect(`watcher|newListener|"x"`, `watcher|removeListener|"x"`)
	})
}
