package plugins

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Not upstream's (docs/plan/chord-plugins-node-evaluator.md 10.4): a Go core facet's keyed member reaches the plugins as a copy of the Go
// object's documents on the Node host. Oracle: Pi's reference.ts ReferenceKeyed (`get` waits while loading and answers the document or
// `undefined`; `at(key)` is `initial()` while the key is absent, a root replacement when it is created, and follows each change), and
// app.test.ts "round-trips a worker's documents, tables and files" (a plugin writes through the kit's `Files` and reads it back).

var kcDocs = func() *documents.StateDefinition {
	definition, err := documents.DefineState("docs", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("text", contracts.Str())), Version: 1, Keyed: true,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("text", "") },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

var kcDocs2 = func() *documents.StateDefinition {
	definition, err := documents.DefineState("docs2", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("text", contracts.Str())), Version: 1, Keyed: true,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("text", "") },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

var kcCore = mustContract(contracts.Local("test.keyedcore", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("docs", contracts.Keyed(contracts.Obj(contracts.Prop("text", contracts.Str())))),
		contracts.Mem("put", contracts.NewMethod(contracts.Obj(contracts.Prop("key", contracts.Str()), contracts.Prop("text", contracts.Str())))),
		contracts.Mem("drop", contracts.NewMethod(contracts.Str())),
	},
}))

// kcOut is what the plugin below serves: the copy's `at("w")`, a read of a key, and the Go object's two methods.
var kcOut = mustContract(contracts.Local("test.keyedout", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("watched", contracts.Str()),
		contracts.Mem("read", contracts.NewMethod(contracts.Str(), contracts.Unknown())),
		contracts.Mem("put", contracts.NewMethod(contracts.Obj(contracts.Prop("key", contracts.Str()), contracts.Prop("text", contracts.Str())))),
		contracts.Mem("drop", contracts.NewMethod(contracts.Str())),
	},
}))

func kcFacet() host.Facet { return kcFacetOf(kcDocs) }

// kcFacetOf is the Go core facet keeping the keyed member's documents in definition: a reload with another definition ends the slot of the
// first (no attempt declares it) and serves a new one.
func kcFacetOf(definition *documents.StateDefinition) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(kcCore, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			docs := self.State(definition).(*documents.Keyed)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Keyed: map[string]contracts.KeyedWatchable[delta.JsonValue]{"docs": host.ExposeKeyed(docs)},
				Methods: map[string]contracts.MethodFunction{
					"put": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						object := args.(*delta.JsonObject)
						_, err := docs.Change(ctx, object.Value("key").(string), func(draft *delta.Object) (any, error) {
							return nil, draft.Set("text", object.Value("text"))
						})
						return nil, err
					},
					"drop": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return nil, docs.Retire(ctx, args.(string))
					},
				},
			}}, nil
		}, host.ServeOptions{})
	})
}

const kcPlugin = `(function (require, module, exports) {
const { defineFacet, keyed, local, method, obj, str, unknown } = require("@earendil-works/chord");
const Core = local("test.keyedcore", { version: 1, params: { id: str() }, members: { docs: keyed(obj({ text: str() })), put: method(obj({ key: str(), text: str() })), drop: method(str()) } });
const Out = local("test.keyedout", { version: 1, params: { id: str() }, members: { watched: str(), read: method(str(), unknown()), put: method(obj({ key: str(), text: str() })), drop: method(str()) } });
module.exports = { default: defineFacet((host) => host.serve(Out, (_params, self) => {
	const core = self.get(Core, { id: "a" });
	return {
		watched: self.derive([core.docs.at("w")], ([doc]) => doc.text),
		read: async (key, ctx) => (await core.docs.get(key, ctx))?.text ?? null,
		put: (args, ctx) => core.put(args, ctx),
		drop: (key, ctx) => core.drop(key, ctx),
	};
})) };
})`

func kcCall(t *testing.T, ref *NodeReference, method string, args delta.JsonValue) delta.JsonValue {
	t.Helper()
	got, err := ref.Call(method, args, background)
	if err != nil {
		t.Fatalf("%s(%v): %v", method, args, err)
	}
	return got
}

// A plugin reads a keyed member of a Go core facet as it reads a reference's keyed member: the documents the Go object holds when the
// plugin's object goes live, a write the plugin awaited, what the Go side commits later (`get` and `at`), and a retirement.
func TestBridgedAKeyedMemberIsCopiedToThePluginsHost(t *testing.T) {
	goHost := bgGoHost(t, host.HostFacet{Name: "@app/keyedcore", Facet: kcFacet()})
	goRef, err := goHost.Get(background, kcCore, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(goRef.Release)
	put := func(key, text string) {
		t.Helper()
		if _, err := goRef.Call("put", delta.JsonObjectOf("key", key, "text", text), background); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "one")
	node := bgNode(t, []BridgedFacet{{Name: "@app/keyedcore", Host: goHost, Contracts: []*contracts.Contract{kcCore}}}, handmade(t, "@test/kplug", "worker", kcPlugin))
	out, err := node.Get(background, kcOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	// Present when the object went live; absent is `undefined` (null through `read`).
	if got := kcCall(t, out, "read", "a"); got != "one" {
		t.Fatalf("read a = %v, want the document the Go object held", got)
	}
	if got := kcCall(t, out, "read", "b"); got != nil {
		t.Fatalf("read b = %v, want absent", got)
	}
	// A write the plugin awaited is read back at once (the call's answer follows its commit's frame).
	kcCall(t, out, "put", delta.JsonObjectOf("key", "b", "text", "two"))
	if got := kcCall(t, out, "read", "b"); got != "two" {
		t.Fatalf("read b after the plugin's put = %v", got)
	}
	kcCall(t, out, "drop", "a")
	if got := kcCall(t, out, "read", "a"); got != nil {
		t.Fatalf("read a after the plugin's drop = %v", got)
	}
	// `at("w")` is `initial()` while absent, then each change of the Go side, then `initial()` again once retired.
	if got := bgRead(t, out, "watched"); got != "" {
		t.Fatalf("watched while absent = %v", got)
	}
	put("w", "x")
	eventually(t, 15*time.Second, "the Go commit to reach the pinned key", func() bool { return bgRead(t, out, "watched") == "x" })
	put("w", "y")
	eventually(t, 15*time.Second, "the second Go commit to reach the pinned key", func() bool { return bgRead(t, out, "watched") == "y" })
	if _, err := goRef.Call("drop", "w", background); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "the retirement to reach the pinned key", func() bool { return bgRead(t, out, "watched") == "" })
	put("c", "three")
	eventually(t, 15*time.Second, "a document created on the Go side to be read", func() bool { return kcCall(t, out, "read", "c") == "three" })
}

// The copy holds each document of the member, so an object over many documents goes live with all of them: 10,000 documents (a Files
// object with 10,000 spaces; a space's summary is one document however many files it holds) are in one `mirror` answer, and a read of
// the first and the last, and of an absent key, is as fast as a read of the few.
func TestBridgedAKeyedMemberOfTenThousandDocumentsIsCopiedBeforeTheObjectIsLive(t *testing.T) {
	const documentCount = 10_000
	goHost := bgGoHost(t, host.HostFacet{Name: "@app/keyedcore", Facet: kcFacet()})
	goRef, err := goHost.Get(background, kcCore, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(goRef.Release)
	for i := range documentCount {
		if _, err := goRef.Call("put", delta.JsonObjectOf("key", fmt.Sprintf("space-%05d", i), "text", fmt.Sprintf("summary %d", i)), background); err != nil {
			t.Fatal(err)
		}
	}
	node := bgNode(t, []BridgedFacet{{Name: "@app/keyedcore", Host: goHost, Contracts: []*contracts.Contract{kcCore}}}, handmade(t, "@test/kplug", "worker", kcPlugin))
	start := time.Now()
	out, err := node.Get(background, kcOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	t.Logf("%d documents copied and live in %v", documentCount, time.Since(start))
	for _, i := range []int{0, 4999, documentCount - 1} {
		if got := kcCall(t, out, "read", fmt.Sprintf("space-%05d", i)); got != fmt.Sprintf("summary %d", i) {
			t.Fatalf("read %d = %v", i, got)
		}
	}
	if got := kcCall(t, out, "read", fmt.Sprintf("space-%05d", documentCount)); got != nil {
		t.Fatalf("read past the last = %v", got)
	}
}

// A commit made after the snapshot was read and before the object is live (the frame follows the mirror's answer in the same chunk) is
// applied after the snapshot: the snapshot neither retires the key the commit made nor puts back the value it changed. The Node process is
// stopped while the commits are made and the frames sent, so the answer and the frames reach it in one read.
func TestBridgedAKeyedMemberKeepsACommitMadeWhileTheObjectOpens(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a process is stopped with a signal on Unix only")
	}
	goHost := bgGoHost(t, host.HostFacet{Name: "@app/keyedcore", Facet: kcFacet()})
	goRef, err := goHost.Get(background, kcCore, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(goRef.Release)
	put := func(key, text string) {
		t.Helper()
		if _, err := goRef.Call("put", delta.JsonObjectOf("key", key, "text", text), background); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "one")
	var node *NodeHost
	hook := func() {
		resume := pauseNode(t, node)
		put("k", "new")
		put("a", "two")
		// The answer of `mirror` and the three frames are written while the process is stopped.
		time.AfterFunc(500*time.Millisecond, resume)
	}
	node = bgNode(t, []BridgedFacet{{Name: "@app/keyedcore", Host: goHost, Contracts: []*contracts.Contract{kcCore}}}, handmade(t, "@test/kplug", "worker", kcPlugin))
	mirrorSnapshotHook.Store(&hook)
	out, err := node.Get(background, kcOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	if got := kcCall(t, out, "read", "k"); got != "new" {
		t.Fatalf("read k = %v, want the commit made while the object opened", got)
	}
	if got := kcCall(t, out, "read", "a"); got != "two" {
		t.Fatalf("read a = %v, want the commit made while the object opened", got)
	}
}

// A Go facet that reloads with the member kept in another definition ends the slot the mirror followed: the mirror reads the reference's
// next slot and the plugin sees what that holds (the documents of the old slot gone), then what the object commits.
func TestBridgedAKeyedMemberIsReadAgainAfterTheGoFacetReloads(t *testing.T) {
	goHost := bgGoHost(t, host.HostFacet{Name: "@app/keyedcore", Facet: kcFacet()})
	goRef, err := goHost.Get(background, kcCore, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(goRef.Release)
	put := func(key, text string) {
		t.Helper()
		if _, err := goRef.Call("put", delta.JsonObjectOf("key", key, "text", text), background); err != nil {
			t.Fatal(err)
		}
	}
	reload := func() {
		t.Helper()
		if _, err := goHost.Reload([]host.HostFacet{{Name: "@app/keyedcore", Facet: kcFacetOf(kcDocs2)}}, background, host.ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "one")
	node := bgNode(t, []BridgedFacet{{Name: "@app/keyedcore", Host: goHost, Contracts: []*contracts.Contract{kcCore}}}, handmade(t, "@test/kplug", "worker", kcPlugin))
	out, err := node.Get(background, kcOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	if got := kcCall(t, out, "read", "a"); got != "one" {
		t.Fatalf("read a = %v", got)
	}
	// A commit made after the mirror read the new slot, before it sent the documents, follows them.
	hook := func() { put("z", "fresh") }
	mirrorSnapshotHook.Store(&hook)
	reload()
	eventually(t, 30*time.Second, "the commit made while the new slot was read", func() bool { return kcCall(t, out, "read", "z") == "fresh" })
	eventually(t, 30*time.Second, "the document of the old slot to be gone", func() bool { return kcCall(t, out, "read", "a") == nil })
	put("y", "after")
	eventually(t, 30*time.Second, "a commit of the object after the reloads", func() bool { return kcCall(t, out, "read", "y") == "after" })
}

// A slot that ends right after a resync sent its reset (the mirror is no longer resyncing) is read again: the plugin sees the third slot's
// documents, not the second's.
func TestBridgedAKeyedMemberIsReadAgainWhenTheNextSlotEndsAfterTheResync(t *testing.T) {
	goHost := bgGoHost(t, host.HostFacet{Name: "@app/keyedcore", Facet: kcFacet()})
	goRef, err := goHost.Get(background, kcCore, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(goRef.Release)
	reload := func(definition *documents.StateDefinition) {
		t.Helper()
		if _, err := goHost.Reload([]host.HostFacet{{Name: "@app/keyedcore", Facet: kcFacetOf(definition)}}, background, host.ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	node := bgNode(t, []BridgedFacet{{Name: "@app/keyedcore", Host: goHost, Contracts: []*contracts.Contract{kcCore}}}, handmade(t, "@test/kplug", "worker", kcPlugin))
	out, err := node.Get(background, kcOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	if got := kcCall(t, out, "read", "a"); got != nil {
		t.Fatalf("read a = %v", got)
	}
	reloaded := make(chan struct{})
	hook := func() {
		reload(kcDocs)
		close(reloaded)
	}
	mirrorResyncedHook.Store(&hook)
	reload(kcDocs2)
	select {
	case <-reloaded:
	case <-time.After(30 * time.Second):
		t.Fatal("the resync never sent its reset")
	}
	if _, err := goRef.Call("put", delta.JsonObjectOf("key", "y", "text", "third"), background); err != nil {
		t.Fatal(err)
	}
	eventually(t, 30*time.Second, "a commit of the third slot", func() bool { return kcCall(t, out, "read", "y") == "third" })
}

// A method's answer waits for the frames of its commits even while a resync reads the slot they were made in: the call made during the
// read answers after the reset and its frame were sent, and the plugin reads what it wrote at once.
func TestBridgedAMethodCalledWhileTheMirrorIsReadAgainAnswersAfterItsFrames(t *testing.T) {
	goHost := bgGoHost(t, host.HostFacet{Name: "@app/keyedcore", Facet: kcFacet()})
	goRef, err := goHost.Get(background, kcCore, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(goRef.Release)
	node := bgNode(t, []BridgedFacet{{Name: "@app/keyedcore", Host: goHost, Contracts: []*contracts.Contract{kcCore}}}, handmade(t, "@test/kplug", "worker", kcPlugin))
	out, err := node.Get(background, kcOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	if got := kcCall(t, out, "read", "a"); got != nil {
		t.Fatalf("read a = %v", got)
	}
	reading, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	hook := func() {
		close(reading)
		<-release
	}
	mirrorSnapshotHook.Store(&hook)
	if _, err := goHost.Reload([]host.HostFacet{{Name: "@app/keyedcore", Facet: kcFacetOf(kcDocs2)}}, background, host.ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	<-reading
	answered := make(chan error, 1)
	go func() {
		_, err := out.Call("put", delta.JsonObjectOf("key", "b", "text", "two"), background)
		answered <- err
	}()
	select {
	case err := <-answered:
		t.Fatalf("put answered (%v) before the frames of its commit were sent", err)
	case <-time.After(500 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case err := <-answered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("put never answered")
	}
	if got := kcCall(t, out, "read", "b"); got != "two" {
		t.Fatalf("read b after the plugin's put = %v, want what it wrote", got)
	}
}
