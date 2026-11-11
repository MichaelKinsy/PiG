package host

// pi: packages/chord/test/examples/09-calls.ts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// --- 09-calls.ts ---------------------------------------------------------------------------------------------------

func TestExample09Calls(t *testing.T) {
	files := exRemote("example.files", contracts.Params{}, exServerAddress, contracts.Var("owner", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{
			contracts.Meth("whoami", contracts.NewMethod(contracts.Schema{}, exStr())),
			// A `bytes()` leaf: the argument and result travel as raw segments, never base64.
			contracts.Meth("upload", contracts.NewMethod(exObj(exProp("name", exStr()), exProp("data", exBytes())), exInt())),
			contracts.Meth("download", contracts.NewMethod(exObj(exProp("name", exStr())), exBytes())),
			contracts.Meth("quota", contracts.NewMethod()),
			contracts.Meth("slow", contracts.NewMethod()),
		},
	}))
	var stored sync.Map
	aborted := make(chan error, 2)
	// reached receives once per slow call whose handler runs at the server: a call that never reached it sends nothing.
	reached := make(chan struct{}, 2)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(files, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return exVariant("owner", nil, map[string]contracts.MethodFunction{
				// ctx.principal is the caller as its link stamped it; ctx.incarnation the calling host's run (R20).
				"whoami": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					handler := exCtx(ctx)
					return fmt.Sprintf("%s from %s…", principalKind(handler.Principal()), handler.Incarnation()[:8]), nil
				},
				"upload": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					bytes := args.Value("data").([]byte)
					stored.Store(args.Value("name"), bytes)
					return float64(len(bytes)), nil
				}),
				"download": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					if value, ok := stored.Load(args.Value("name")); ok {
						return value.([]byte), nil
					}
					return []byte{}, nil
				}),
				// A PluginError crosses with its code, message and details; any other throw as code `internal`.
				"quota": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					return nil, data.NewPluginError("quota", "the store is full", jsonObject("limit", float64(1)))
				},
				// A handler's context is aborted when its caller cancels or releases the reference.
				"slow": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					reached <- struct{}{}
					return exBlocked(func(cause error) { aborted <- cause })(args, ctx)
				},
			}), nil
		}, ServeOptions{})
	})
	server, _ := exServer(t, nil, HostFacet{Name: "@example/files", Facet: facet})
	clientErrors := &hostErrors{}
	client := newHost(t, HostOptions{OnError: clientErrors.push})
	exTwoPartyHosts(t, server, client, ownerPrincipal())
	mustLoad(t)(client.Load(nil, background))
	reference := exGet(t, client, files, jsonObject())
	whoami := exCall(t, reference, "whoami", nil).(string)
	if !strings.HasPrefix(whoami, "owner from ") || len([]rune(strings.TrimPrefix(whoami, "owner from "))) != 9 || !strings.HasSuffix(whoami, "…") {
		t.Fatalf("whoami = %q", whoami)
	}
	payload := make([]byte, 1024*1024)
	for i := range payload {
		payload[i] = 7
	}
	exEqual(t, "uploaded bytes", exCall(t, reference, "upload", jsonObject("name", "chart.png", "data", payload)), float64(1024*1024))
	back, ok := exCall(t, reference, "download", jsonObject("name", "chart.png")).([]byte)
	if !ok || len(back) != 1024*1024 {
		t.Fatalf("downloaded: %T of %d bytes", back, len(back))
	}
	_, err := reference.Call("upload", jsonObject("name", float64(1), "data", payload), background)
	_ = exCode(t, err, data.CodeInvalid, data.ReasonArgs)
	_, err = reference.Call("quota", nil, background)
	var plugin *data.PluginError
	if !errors.As(err, &plugin) || plugin.Code != "quota" || plugin.Message != "the store is full" {
		t.Fatalf("quota: %v", err)
	}
	exEqual(t, "plugin details", plugin.Details, exJSON(t, `{"limit":1}`))
	// A cancelled call rejects with the cancel's error and aborts the handler.
	cancellable, cancel := chordctx.WithCancel(background)
	slowDone := make(chan error, 1)
	go func() { _, err := reference.Call("slow", nil, cancellable); slowDone <- err }()
	<-reached
	cancel(errors.New("the user gave up"))
	if err := <-slowDone; err == nil || err.Error() != "the user gave up" {
		t.Fatalf("client: slow rejected: %v", err)
	}
	if cause := <-aborted; cause == nil {
		t.Fatal("server: slow was aborted without a reason")
	}
	// Releasing the reference aborts a pending call.
	pendingDone := make(chan error, 1)
	go func() { _, err := reference.Call("slow", nil, background); pendingDone <- err }()
	// The handler of this call runs. The activity count cannot say so: the first call's may not have drained yet, so a
	// release could come before this call was made and reject it without it reaching the server, and `aborted` never fires.
	<-reached
	reference.Release()
	_ = exCode(t, <-pendingDone, data.CodeReleased, "")
	<-aborted
	exCloseHost(t, client)
	exCloseHost(t, server)
	if clientErrors.Len() > 0 {
		t.Fatalf("client errors: %v", clientErrors.All())
	}
}

// exActivity is `host.activity.value`.
func exActivity(t testing.TB, host Host) ActivityCounts {
	t.Helper()
	var value delta.JsonValue
	var err error
	// Pi reads it inside a section of the host: so does the test.
	host.(*chordHost).enter(func() { value, err = host.Activity().Value() })
	must(t, err)
	object := value.(*delta.JsonObject)
	count := func(key string) int { number, _ := object.Value(key).(float64); return int(number) }
	return ActivityCounts{References: count("references"), Calls: count("calls"), Jobs: count("jobs"), Lingering: count("lingering")}
}

// exStateKinds starts a `ref.state` watch and collects each state as Pi's `describe` prints it: its kind, and for a
// failed one its cause's kind, whether it retries by itself, and its error's message.
func exStateKinds(t testing.TB, reference *Reference) (chord.NativeWatchHandle[*RefState], *exCollector) {
	t.Helper()
	watch, err := reference.State().Watch(background)
	must(t, err)
	collected := &exCollector{}
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[*RefState]) error {
		if call.Kind == chord.WatchCallValue {
			collected.add(exDescribe(call.Value))
		}
		return nil
	})
	return watch, collected
}

// exDescribe is a state for the log: a failure with its cause's kind, when it retries by itself, and its error.
func exDescribe(current *RefState) string {
	if current.Kind != RefStateFailed {
		return string(current.Kind)
	}
	retry := "no automatic retry"
	if current.RetryAt != nil {
		retry = "retries"
	}
	return fmt.Sprintf("failed, %s, %s: %s", current.Cause.Kind, retry, current.Cause.Error.Message)
}

// exWaitState waits until `describe(ref.state.value)` is want; it fails with the last state seen.
func exWaitState(t testing.TB, reference *Reference, want string) {
	t.Helper()
	last, logged := "", ""
	scopeEventually(t, "the state "+want, func() bool {
		last = exStateNow(t, reference)
		if last != want && last != logged {
			logged = last
			t.Logf("state is %q", last)
		}
		return last == want
	})
}

// exParked is the number of calls parked on a reference that waits for its object (`parked.size`); read in a section.
func exParked(host Host, reference *Reference) int {
	size := 0
	host.(*chordHost).enter(func() {
		if parked := reference.binding.parked; parked != nil {
			size = parked.size
		}
	})
	return size
}

// exStateNow is `describe(ref.state.value)`.
func exStateNow(t testing.TB, reference *Reference) string {
	t.Helper()
	return exDescribe(reference.State().Value())
}
