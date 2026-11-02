package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// --- 33-reload.ts --------------------------------------------------------------------------------------------------

func TestExample33Reload(t *testing.T) {
	// The notes store: a list, its banner, and `add` (which can be told to wait, to show a call across a swap).
	notes := exLocal("example.notes", contracts.Params{},
		contracts.Mem("list", exArr(exStr())), contracts.Mem("banner", exStr()),
		contracts.Mem("add", contracts.NewMethod(exObj(exProp("text", exStr())), exInt())))
	// A search index over the notes, holding a native (an index file, say).
	search := exLocal("example.search", contracts.Params{}, contracts.Mem("engine", exStr()), contracts.Mem("reindex", contracts.NewMethod()))
	// A clock plugin, removed by the third reload.
	clock := exLocal("example.clock", contracts.Params{}, contracts.Mem("zone", exStr()))
	list := exStringListDoc("example.list")
	var logs seenList[string]
	var slowAdd, slowReindex atomic.Pointer[chan struct{}]
	var addWaiting atomic.Int32
	gate := func(slot *atomic.Pointer[chan struct{}], ctx context.Context) {
		if open := slot.Load(); open != nil {
			_, _ = chordctx.Receive(ctx, *open)
		}
	}
	// The notes plugin, its banner and the text `add` prefixes differing per version: a pure object (no `self.open`).
	notesPlugin := func(banner string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(notes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				doc := stateOf(self, list)
				return exImpl(map[string]chord.Watchable[delta.JsonValue]{
					"list": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("list"), nil
					}),
					"banner": exConstant(banner),
				}, map[string]contracts.MethodFunction{
					"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						if slowAdd.Load() != nil {
							addWaiting.Add(1)
						}
						gate(&slowAdd, ctx)
						return exAdd(doc, exParam(args, "text")+" ("+banner+")", ctx)
					}),
				}), nil
			}, ServeOptions{})
		})
	}
	// The search plugin: its engine is a native, so every reload of it restarts the object.
	searchPlugin := func(engine string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(search, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				index := self.Open(func(context.Context) (any, error) {
					logs.add("search: " + engine + " opened")
					return engine, nil
				}, func(value any, reason NativeCloseReason, _ context.Context) error {
					logs.add(fmt.Sprintf("search: %v closed (%s)", value, reason.Kind))
					return nil
				})
				return exImpl(map[string]chord.Watchable[delta.JsonValue]{"engine": exConstant(engine)}, map[string]contracts.MethodFunction{
					"reindex": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						value, _ := index.Value()
						logs.add(fmt.Sprintf("search: reindex on %v started", value))
						gate(&slowReindex, ctx)
						logs.add(fmt.Sprintf("search: reindex on %v done", value))
						return nil, nil
					}),
				}), nil
			}, ServeOptions{})
		})
	}
	clockPlugin := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(clock, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"zone": exConstant("UTC")}, nil), nil
		}, ServeOptions{})
	})
	// 1. A host with three plugins, loaded once. A reload passes the complete set; a facet passed as the very same object
	// is an unchanged plugin (untouched), any other value for a loaded name a changed one.
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: errs.push})
	searchV1 := searchPlugin("lucene")
	notesV2 := notesPlugin("v2")
	searchV2 := searchPlugin("tantivy")
	mustLoad(t)(host.Load([]HostFacet{{Name: "@example/notes", Facet: notesPlugin("v1")}, {Name: "@example/search", Facet: searchV1}, {Name: "@example/clock", Facet: clockPlugin}}, background))
	ref := exGet(t, host, notes, jsonObject())
	exCall(t, ref, "add", jsonObject("text", "first"))
	exEqual(t, "1. list", exValue(t, ref, "list"), exJSON(t, `["first (v1)"]`))
	exEqual(t, "1. banner", exValue(t, ref, "banner"), "v1")
	// 2. A pure swap while a call runs: the running `add` finishes on the old code, the next call runs on the new one.
	open := make(chan struct{})
	slowAdd.Store(&open)
	running := make(chan any, 1)
	go func() {
		result, err := ref.Call("add", jsonObject("text", "second"), background)
		must(t, err)
		running <- result
	}()
	scopeEventually(t, "the slow add running", func() bool { return addWaiting.Load() == 1 })
	swap, err := host.Reload([]HostFacet{{Name: "@example/notes", Facet: notesV2}, {Name: "@example/search", Facet: searchV1}, {Name: "@example/clock", Facet: clockPlugin}}, background, ReloadOptions{})
	must(t, err)
	wantReloadPlugins(t, swap, nil, []string{"@example/notes"}, nil)
	if want := []ReloadedObject{{Object: "example.notes", Kind: ReloadSwapped}}; !reflect.DeepEqual(swap.Objects, want) {
		t.Fatalf("2. objects = %+v, want %+v", swap.Objects, want)
	}
	close(open)
	slowAdd.Store(nil)
	exEqual(t, "2. the old call returned", exReceive(t, "the old call's result", running), float64(2))
	exEqual(t, "2. the new code adds", exCall(t, ref, "add", jsonObject("text", "third")), float64(3))
	exEqual(t, "2. list", exValue(t, ref, "list"), exJSON(t, `["first (v1)","second (v1)","third (v2)"]`))
	exEqual(t, "2. banner", exValue(t, ref, "banner"), "v2")
	// 3. A restart: the search object holds a native. Its running call drains first, then the native closes (reload).
	index := exGet(t, host, search, jsonObject())
	reindexOpen := make(chan struct{})
	slowReindex.Store(&reindexOpen)
	reindexing := make(chan error, 1)
	go func() {
		_, err := index.Call("reindex", nil, background)
		reindexing <- err
	}()
	scopeEventually(t, "the reindex started", func() bool { return slices.Contains(logs.all(), "search: reindex on lucene started") })
	restart, err := host.Reload([]HostFacet{{Name: "@example/notes", Facet: notesV2}, {Name: "@example/search", Facet: searchV2}, {Name: "@example/clock", Facet: clockPlugin}}, background, ReloadOptions{})
	must(t, err)
	if want := []ReloadedObject{{Object: "example.search", Kind: ReloadRestarted}}; !reflect.DeepEqual(restart.Objects, want) {
		t.Fatalf("3. objects = %+v, want %+v", restart.Objects, want)
	}
	// "3. reload: ... - the reference shows": a restart drains first, and the reference shows `loading` meanwhile.
	exEqual(t, "3. the reference shows", exStateNow(t, index), "loading")
	close(reindexOpen)
	slowReindex.Store(nil)
	must(t, exReceive(t, "the reindex call's result", reindexing))
	exWaitState(t, index, "live")
	exEqual(t, "3. search now on", exValue(t, index, "engine"), "tantivy")
	// The log's order is the example's output: the running call finishes on the old native, which closes (reload) before
	// the new one opens.
	wantLog := []string{
		"search: lucene opened", "search: reindex on lucene started", "search: reindex on lucene done",
		"search: lucene closed (reload)", "search: tantivy opened",
	}
	if got := logs.all(); !reflect.DeepEqual(got, wantLog) {
		t.Fatalf("3. log = %q, want %q", got, wantLog)
	}
	// 4. A removal: the clock plugin is gone. Its object ends; its holder's request is not ended, it waits `not_served`.
	held := exHold(t, host, clock, jsonObject())
	exWaitState(t, held, "live")
	removal, err := host.Reload([]HostFacet{{Name: "@example/notes", Facet: notesV2}, {Name: "@example/search", Facet: searchV2}}, background, ReloadOptions{})
	must(t, err)
	wantReloadPlugins(t, removal, nil, nil, []string{"@example/clock"})
	exWaitState(t, held, "failed, waiting, no automatic retry: example.clock is not served here")
	exEqual(t, "4. its last value stays readable", exValue(t, held, "zone"), "UTC")
	// 5. A later reload serves the clock again: the same holder binds again by itself.
	_, err = host.Reload([]HostFacet{{Name: "@example/notes", Facet: notesV2}, {Name: "@example/search", Facet: searchV2}, {Name: "@example/clock", Facet: clockPlugin}}, background, ReloadOptions{})
	must(t, err)
	exWaitState(t, held, "live")
	exEqual(t, "5. clock holder", exValue(t, held, "zone"), "UTC")
	// 6. A staging error is reported in `failed` and changes nothing: the plugin keeps its old code.
	broken := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, errors.New("a typo in the new factory")
		}, ServeOptions{})
	})
	failed, err := host.Reload([]HostFacet{{Name: "@example/notes", Facet: broken}}, background, ReloadOptions{})
	must(t, err)
	if len(failed.Failed) != 1 || !strings.Contains(failed.Failed[0].Error.Error(), "a typo in the new factory") {
		t.Fatalf("6. failed = %v", failedMessages(failed.Failed))
	}
	exEqual(t, "6. banner still", exValue(t, ref, "banner"), "v2")
	held.Release()
	index.Release()
	ref.Release()
	exCloseHost(t, host)
	for _, err := range errs.All() {
		t.Errorf("reported: %s", err.Kind)
	}
}
