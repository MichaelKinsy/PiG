package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Not upstream's (docs/plan/chord-plugins-node-evaluator.md 10.1 and 10.3): the app's Go code holds a local type its plugins
// serve through NodeHost.Get, as Pi's app `get`s one in process (`server.get(Index)`). Oracle: Pi's own `host.get` and its
// references, which the Node host runs for it; these cases port what `reference.test.ts` and `host.test.ts` say of
// `get`, a reference's members, its state and its release to the Go side of the boundary.

// rfThingModule is a core facet serving `test.thing`: a local type with a constant, a derived count, a keyed member and
// the methods that read and change them (the oracle is the fingerprint: the Go contract below declares the same members).
const rfThingModule = `import { ChordError, bytes, constant, defineFacet, defineState, int, keyed, local, method, obj, str, unknown } from "@earendil-works/chord/next";
const Thing = local("test.thing", {
	version: 1,
	params: { id: str() },
	members: {
		label: str(),
		count: int(),
		notes: keyed(obj({ title: str() })),
		add: method(int(), int()),
		reset: method(),
		setNote: method(obj({ key: str(), title: str() })),
		fail: method(),
		forbid: method(),
		wait: method(),
		bin: bytes(),
		echoBytes: method(bytes(), bytes()),
		echoAny: method(unknown(), unknown()),
	},
});
const Count = defineState("count", { schema: obj({ n: int() }), version: 1, initial: () => ({ n: 0 }) });
const Notes = defineState("notes", { schema: obj({ title: str() }), version: 1, initial: () => ({ title: "" }), keyed: true });
const Audit = local("test.audit", { version: 1, params: {}, members: { things: int() } });
export default defineFacet((host) => {
	const things = host.references(Thing);
	host.serve(Audit, (_params, self) => ({ things: self.derive([things], ([list]) => list.length) }));
	host.serve(Thing, ({ id }, self) => {
		// The object "gated" never becomes live: a get of it waits, holding the reference that the audit counts.
		if (id === "gated") self.open(() => new Promise(() => {}), () => {});
		const doc = self.state(Count);
		const notes = self.state(Notes);
		return {
			label: constant("thing-" + id),
			count: self.derive([doc], ([value]) => value.n),
			notes,
			add: async (by, ctx) => {
				await doc.change((draft) => {
					draft.n += by;
				}, ctx);
				return doc.value.n;
			},
			reset: async (ctx) => {
				await doc.change((draft) => {
					draft.n = 0;
				}, ctx);
			},
			setNote: async ({ key, title }, ctx) => {
				await notes.change(key, (draft) => {
					draft.title = title;
				}, ctx);
			},
			fail: async () => {
				throw new Error("it failed");
			},
			forbid: async () => {
				throw new ChordError({ code: "forbidden", message: "not for you" });
			},
			bin: constant(new Uint8Array([1, 2, 3])),
			echoBytes: async (value) => value,
			echoAny: async (value) => value,
			wait: async (ctx) => {
				// The count -1 tells the test the call runs and waits.
				await doc.change((draft) => {
					draft.n = -1;
				}, ctx);
				await new Promise((_, reject) => ctx.abortSignal.addEventListener("abort", () => reject(ctx.abortSignal.reason)));
			},
		};
	});
});
`

// rfThing is the Go declaration of `test.thing`: the same members, so the same fingerprint.
var rfThing = mustContract(contracts.Local("test.thing", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("label", contracts.Str()),
		contracts.Mem("count", contracts.Int()),
		contracts.Mem("notes", contracts.Keyed(contracts.Obj(contracts.Prop("title", contracts.Str())))),
		contracts.Mem("add", contracts.NewMethod(contracts.Int(), contracts.Int())),
		contracts.Mem("reset", contracts.NewMethod()),
		contracts.Mem("setNote", contracts.NewMethod(contracts.Obj(contracts.Prop("key", contracts.Str()), contracts.Prop("title", contracts.Str())))),
		contracts.Mem("fail", contracts.NewMethod()),
		contracts.Mem("forbid", contracts.NewMethod()),
		contracts.Mem("wait", contracts.NewMethod()),
		contracts.Mem("bin", contracts.Bytes()),
		contracts.Mem("echoBytes", contracts.NewMethod(contracts.Bytes(), contracts.Bytes())),
		contracts.Mem("echoAny", contracts.NewMethod(contracts.Unknown(), contracts.Unknown())),
	},
}))

// rfAudit is `test.audit`: how many references to a `test.thing` the Node host has (`host.references`, the presence list of
// Pi's §19), which counts the holds of the reference RPC.
var rfAudit = mustContract(contracts.Local("test.audit", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("things", contracts.Int())},
}))

// rfMissing is a local type nothing serves.
var rfMissing = mustContract(contracts.Local("test.missing", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str())},
}))

// rfNode starts a Node host that serves `test.thing` from a core facet and attaches its link to a Go host, closed when the
// test ends.
func rfNode(t *testing.T, options ...func(*RuntimeLoad)) *NodeHost {
	t.Helper()
	file := filepath.Join(t.TempDir(), "thing.mjs")
	if err := os.WriteFile(file, []byte(rfThingModule), 0o644); err != nil {
		t.Fatal(err)
	}
	load := RuntimeLoad{
		Kind: "server", Core: []CoreFacet{{Name: "@test/thing", Module: file}}, Principal: serverPrincipal, Peer: serverPrincipal,
	}
	for _, option := range options {
		option(&load)
	}
	node, _, err := NewPluginRuntime(NodeOptions{}).Load(load, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	acceptorOf(t, node, serverPrincipal)
	return node
}

func rfParams(id string) *delta.JsonObject { return delta.JsonObjectOf("id", id) }

func rfGet(t *testing.T, node *NodeHost) *NodeReference {
	t.Helper()
	ref, err := node.Get(background, rfThing, rfParams("a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return ref
}

// rfRead reads a state member.
func rfRead(t *testing.T, ref *NodeReference, member string) delta.JsonValue {
	t.Helper()
	value, err := ref.Member(member).Value()
	if err != nil {
		t.Fatalf("%s: %v", member, err)
	}
	return value
}

// rfReferences waits until the Node host has n references to `test.thing` (rfAudit: its presence list counts every hold, the
// reference RPC's included).
func rfReferences(t *testing.T, node *NodeHost, n int) {
	t.Helper()
	audit, err := node.Get(background, rfAudit, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Release()
	eventually(t, 15*time.Second, "the Node host to have the references", func() bool {
		return rfRead(t, audit, "things") == float64(n)
	})
}

// A reference reads a constant and a derived member, calls methods with and without arguments, and a method's result is
// data (reference.test.ts: members, methods; the params reach the factory).
func TestNodeGetReadsAMemberAndCallsAMethodOfALocalTypeThePluginsServe(t *testing.T) {
	ref := rfGet(t, rfNode(t))
	if got := rfRead(t, ref, "label"); got != "thing-a" {
		t.Fatalf("label = %v, want thing-a", got)
	}
	if got := rfRead(t, ref, "count"); got != float64(0) {
		t.Fatalf("count = %v, want 0", got)
	}
	if got, err := ref.Call("add", float64(5), background); err != nil || got != float64(5) {
		t.Fatalf("add(5) = %v, %v", got, err)
	}
	if got := rfRead(t, ref, "count"); got != float64(5) {
		t.Fatalf("count after add = %v, want 5", got)
	}
	if got, err := ref.Call("reset", nil, background); err != nil || got != nil {
		t.Fatalf("reset = %v, %v", got, err)
	}
	if got := rfRead(t, ref, "count"); got != float64(0) {
		t.Fatalf("count after reset = %v, want 0", got)
	}
	if ref.Member("nope") != nil || ref.Keyed("count") != nil || ref.Member("notes") != nil {
		t.Fatal("a name the contract has no state member or keyed member of has a member")
	}
	if _, err := ref.Call("count", nil, background); err == nil {
		t.Fatal("a state member was called")
	}
}

// A method's arguments are checked by the Node host as Pi checks them: invalid args fail `invalid`, a throw is an error with
// its message and a ChordError keeps its code, as *NodeError.
func TestNodeGetCallErrorsCrossAsNodeErrorsWithTheChordError(t *testing.T) {
	ref := rfGet(t, rfNode(t))
	_, err := ref.Call("add", "not a number", background)
	var invalid *data.ChordError
	if !errors.As(err, &invalid) || invalid.Code != data.CodeInvalid {
		t.Fatalf("add(text) = %v, want invalid", err)
	}
	_, err = ref.Call("fail", nil, background)
	var thrown *NodeError
	if !errors.As(err, &thrown) || thrown.Message != "it failed" {
		t.Fatalf("fail = %v, want the plugin's error", err)
	}
	_, err = ref.Call("forbid", nil, background)
	var forbidden *data.ChordError
	if !errors.As(err, &forbidden) || forbidden.Code != data.CodeForbidden || forbidden.Message != "not for you" {
		t.Fatalf("forbid = %v, want forbidden: not for you", err)
	}
}

// A watch holds the current value and gets one frame per change, in order, with the ops that turn the previous value into
// the new one (watch.test.ts's frames); the member's watch ends `ended` when the reference is released.
func TestNodeGetWatchesAMemberInOrder(t *testing.T) {
	ref := rfGet(t, rfNode(t))
	watch, err := ref.Member("count").Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	if got := watch.Value(); got != float64(0) {
		t.Fatalf("the watch starts at %v, want 0", got)
	}
	var mu sync.Mutex
	var seen []float64
	var ops []delta.Op
	got := make(chan struct{}, 64)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		mu.Lock()
		defer mu.Unlock()
		if call.Kind != chord.WatchCallValue {
			t.Errorf("a %s call arrived", call.Kind)
			return nil
		}
		seen = append(seen, call.Value.(float64))
		ops = append(ops, call.Ops...)
		got <- struct{}{}
		return nil
	})
	const calls = 20
	for i := 1; i <= calls; i++ {
		if _, err := ref.Call("add", float64(1), background); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, 15*time.Second, "the last frame", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) > 0 && seen[len(seen)-1] == calls
	})
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(seen); i++ {
		if seen[i] <= seen[i-1] {
			t.Fatalf("frames out of order: %v", seen)
		}
	}
	for _, op := range ops {
		if delta.Verb(op) != "r" {
			t.Fatalf("op = %v, want a replacement of the number", op)
		}
	}
	if len(seen) == 0 || len(ops) == 0 {
		t.Fatal("no frame arrived")
	}
	ref.Release()
	select {
	case <-watch.Closed():
	case <-time.After(15 * time.Second):
		t.Fatal("the member's watch did not end with its reference")
	}
	if end := watch.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("end = %+v, want ended", end)
	}
}

// A watch that was stopped stops on the Node host too: the frames of a later change do not reach another watch of its id, and
// a stop is idempotent; a watch aborted by its context ends `aborted`.
func TestNodeGetAWatchStoppedOrAbortedEndsHereAndThere(t *testing.T) {
	ref := rfGet(t, rfNode(t))
	stopped, err := ref.Member("count").Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	if end, err := stopped.Stop(); err != nil || end.Kind != chord.WatchStopped {
		t.Fatalf("Stop = %+v, %v", end, err)
	}
	if end, err := stopped.Stop(); err != nil || end.Kind != chord.WatchStopped {
		t.Fatalf("a second Stop = %+v, %v", end, err)
	}
	ctx, cancel := context.WithCancel(background)
	aborted, err := ref.Member("count").Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-aborted.Closed():
	case <-time.After(15 * time.Second):
		t.Fatal("the watch did not end with its context")
	}
	if end := aborted.End(); end.Kind != chord.WatchAborted {
		t.Fatalf("end = %+v, want aborted", end)
	}
	// What the Node host still holds for the reference is released with it, exactly: nothing is left counted.
	ref.Release()
}

// A keyed member reads a document by key (absent: found false) and watches one, nil while it is absent.
func TestNodeGetReadsAndWatchesAKeyedMember(t *testing.T) {
	ref := rfGet(t, rfNode(t))
	notes := ref.Keyed("notes")
	if value, found, err := notes.Get(background, "a"); err != nil || found || value != nil {
		t.Fatalf("an absent note = %v, %v, %v", value, found, err)
	}
	if watch, err := notes.Watch(background, "a"); err != nil || watch != nil {
		t.Fatalf("a watch of an absent note = %v, %v", watch, err)
	}
	if _, err := ref.Call("setNote", delta.JsonObjectOf("key", "a", "title", "one"), background); err != nil {
		t.Fatal(err)
	}
	value, found, err := notes.Get(background, "a")
	if err != nil || !found || !reflect.DeepEqual(value, delta.JsonObjectOf("title", "one")) {
		t.Fatalf("note a = %v, %v, %v", value, found, err)
	}
	watch, err := notes.Watch(background, "a")
	if err != nil || watch == nil {
		t.Fatalf("a watch of note a = %v, %v", watch, err)
	}
	frames := pcStartFrames(watch)
	if _, err := ref.Call("setNote", delta.JsonObjectOf("key", "a", "title", "two"), background); err != nil {
		t.Fatal(err)
	}
	frames.count(t, 1)
	if got := frames.all(); !reflect.DeepEqual(got, []delta.JsonValue{delta.JsonObjectOf("title", "two")}) {
		t.Fatalf("frames = %v, want [{title: two}]", got)
	}
}

// Release is exact: the Node host's count of references (`host.activity`) is one while the reference is held and none after
// its release, whichever way the references were made (two gets are two holds), and a second release changes nothing.
func TestNodeGetReleaseIsExact(t *testing.T) {
	node := rfNode(t)
	rfReferences(t, node, 0)
	first, err := node.Get(background, rfThing, rfParams("a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := node.Get(background, rfThing, rfParams("a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rfReferences(t, node, 2)
	first.Release()
	first.Release()
	rfReferences(t, node, 1)
	second.Release()
	rfReferences(t, node, 0)
	// After its release a reference is ended and every use fails `released`.
	if state := first.State().Value(); state.Kind != host.RefStateEnded || state.Error.Code != data.CodeReleased {
		t.Fatalf("state after release = %+v, want ended released", state)
	}
	if _, err := first.Call("add", float64(1), background); !isReleased(err) {
		t.Fatalf("a call after release = %v, want released", err)
	}
	if _, err := first.Member("count").Value(); !isReleased(err) {
		t.Fatalf("a read after release = %v, want released", err)
	}
	if _, err := first.Member("count").Watch(background); !isReleased(err) {
		t.Fatalf("a watch after release = %v, want released", err)
	}
}

func isReleased(err error) bool {
	var chordErr *data.ChordError
	return errors.As(err, &chordErr) && chordErr.Code == data.CodeReleased
}

// The reference's state follows the Node host's: live once its object is, with every change in order for a watch, and ended
// when it is released.
func TestNodeGetStateIsLiveAndAWatchEndsWithTheRelease(t *testing.T) {
	ref := rfGet(t, rfNode(t))
	if state := ref.State().Value(); !reflect.DeepEqual(state, &host.RefState{Kind: host.RefStateLive}) {
		t.Fatalf("state = %+v, want live", state)
	}
	watch, err := ref.State().Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	ref.Release()
	select {
	case <-watch.Closed():
	case <-time.After(15 * time.Second):
		t.Fatal("the state's watch did not end with the release")
	}
	if end := watch.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("end = %+v, want ended", end)
	}
}

// `get` waits while the object is not live until the ReadyTimeout of its options, then fails with Pi's error (`unavailable`);
// a type no plugin serves is that case (host.test.ts, "a type not served here").
func TestNodeGetWaitsForTheReadyTimeoutThenFailsUnavailable(t *testing.T) {
	node := rfNode(t)
	short := 400 * time.Millisecond
	start := time.Now()
	ref, err := node.Get(background, rfMissing, delta.NewJsonObject(0), host.GetOptions{ReadyTimeout: &short})
	if err == nil {
		ref.Release()
		t.Fatal("a type nothing serves was got")
	}
	if elapsed := time.Since(start); elapsed < short || elapsed > 20*time.Second {
		t.Fatalf("it failed after %v, want after the %v ready timeout", elapsed, short)
	}
	var chordErr *data.ChordError
	if !errors.As(err, &chordErr) || chordErr.Code != data.CodeUnavailable {
		t.Fatalf("error = %v, want unavailable", err)
	}
	if _, ok := errors.AsType[*NodeError](err); !ok {
		t.Fatalf("error = %T, want a *NodeError", err)
	}
	rfReferences(t, node, 0)
}

// Aborting the context of a `get` that waits aborts it on the Node host: it fails `aborted` at once and holds nothing.
func TestNodeGetAbortsWhenItsContextDoes(t *testing.T) {
	node := rfNode(t)
	long := time.Minute
	ctx, cancel := context.WithCancel(background)
	result := make(chan error, 1)
	go func() {
		ref, err := node.Get(ctx, rfThing, rfParams("gated"), host.GetOptions{ReadyTimeout: &long})
		if err == nil {
			ref.Release()
		}
		result <- err
	}()
	// The Node host holds the waiting get's reference: the request is running when the abort is sent.
	rfReferences(t, node, 1)
	cancel()
	select {
	case err := <-result:
		var nodeErr *NodeError
		if !errors.Is(err, context.Canceled) || !errors.As(err, &nodeErr) {
			t.Fatalf("error = %v, want the caller's abort and the Node host's rejection", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the aborted get did not return")
	}
	rfReferences(t, node, 0)
}

// A call that waits is aborted with its context, on the Node host: the method's own signal aborts.
func TestNodeGetACallAbortsWhenItsContextDoes(t *testing.T) {
	ref := rfGet(t, rfNode(t))
	ctx, cancel := context.WithCancel(background)
	result := make(chan error, 1)
	go func() {
		_, err := ref.Call("wait", nil, ctx)
		result <- err
	}()
	eventually(t, 15*time.Second, "the method to run and wait", func() bool { return rfRead(t, ref, "count") == float64(-1) })
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("the aborted call succeeded")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the aborted call did not return")
	}
}

// The contract is Pi's own check on the Node host: a contract of another fingerprint (a different declaration of the same
// type) is refused by the host's binding, and a remote contract is not held through this API.
func TestNodeGetRefusesAContractThePluginsDoNotServeAndARemoteOne(t *testing.T) {
	node := rfNode(t)
	other := mustContract(contracts.Local("test.thing", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
		Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Int())},
	}))
	short := 300 * time.Millisecond
	ref, err := node.Get(background, other, rfParams("a"), host.GetOptions{ReadyTimeout: &short})
	if err == nil {
		ref.Release()
		t.Fatal("a contract of another fingerprint was got")
	}
	var chordErr *data.ChordError
	if !errors.As(err, &chordErr) || chordErr.Code != data.CodeContractMismatch {
		t.Fatalf("error = %v, want contract_mismatch", err)
	}
	if _, err := node.Get(background, notesContract, delta.NewJsonObject(0), host.GetOptions{}); err == nil {
		t.Fatal("a remote contract was got through the control channel")
	}
	if _, err := node.Get(background, rfThing, delta.JsonObjectOf("id", float64(1)), host.GetOptions{}); err == nil {
		t.Fatal("params that do not match the contract were accepted")
	}
}

// A contract that crossed with a fingerprint its rebuilt declaration does not have is refused before it is bound.
func TestNodeGetRefusesContractDataThatHashesDifferently(t *testing.T) {
	node := rfNode(t)
	wire, err := ContractDataOf(rfThing)
	if err != nil {
		t.Fatal(err)
	}
	wire.Fingerprint = "0000000000000000"
	call, err := node.send("describe", &describeRequest{Contract: wire})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.await(call, background); err != nil {
		t.Fatalf("describe reports what it rebuilt, unverified: %v", err)
	}
	reference := newNodeReference(node, rfThing, mustDeclared(t, rfThing))
	t.Cleanup(reference.forget)
	call, err = node.send("get", &getRequest{Contract: wire, Params: []byte(`{"id":"a"}`), Handle: reference.handle})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.await(call, background)
	var nodeErr *NodeError
	if !errors.As(err, &nodeErr) || nodeErr.Message == "" {
		t.Fatalf("error = %v, want the host's refusal", err)
	}
}

func mustDeclared(t *testing.T, contract *contracts.Contract) *contracts.ContractData {
	t.Helper()
	declared, err := contracts.ContractDataOf(contract)
	if err != nil {
		t.Fatal(err)
	}
	return declared
}

// When the process ends, a reference is `loading` for good, as a lost link's (its watch of the state gets that frame), and its
// calls and reads fail with the process's exit.
func TestNodeGetAProcessThatEndsLeavesTheReferenceLoading(t *testing.T) {
	node := rfNode(t)
	ref := rfGet(t, node)
	states, err := ref.State().Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var kinds []host.RefStateKind
	states.Start(func(_ context.Context, call chord.NativeWatchCall[*host.RefState]) error {
		mu.Lock()
		kinds = append(kinds, call.Value.Kind)
		mu.Unlock()
		return nil
	})
	node.kill()
	<-node.Exited()
	eventually(t, 15*time.Second, "the reference to be loading", func() bool { return ref.State().Value().Kind == host.RefStateLoading })
	eventually(t, 15*time.Second, "the state watch to get the frame", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return reflect.DeepEqual(kinds, []host.RefStateKind{host.RefStateLoading})
	})
	_, err = ref.Call("add", float64(1), background)
	var exit *NodeExit
	if !errors.As(err, &exit) {
		t.Fatalf("a call after the process ended = %v, want its *NodeExit", err)
	}
	if _, err := ref.Member("count").Value(); !errors.As(err, &exit) {
		t.Fatalf("a read after the process ended = %v, want its *NodeExit", err)
	}
	if _, err := node.Get(background, rfThing, rfParams("b"), host.GetOptions{}); !errors.As(err, &exit) {
		t.Fatalf("a get after the process ended = %v, want its *NodeExit", err)
	}
	ref.Release()
	if state := ref.State().Value(); state.Kind != host.RefStateEnded {
		t.Fatalf("state after release = %+v, want ended", state)
	}
}
