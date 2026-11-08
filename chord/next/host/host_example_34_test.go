package host

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// --- 34-versions.ts ------------------------------------------------------------------------------------------------

func TestExample34Versions(t *testing.T) {
	// The shared contract, as every plugin bundles it: v1, then v2 where a note gained a tag.
	notesV1 := exRemote("example.notes", contracts.Params{}, exServerAddress, contracts.Var("editor", contracts.VariantDeclaration{
		State:   []contracts.Property{exProp("notes", exArr(exStr()))},
		Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr())), exInt()))},
	}))
	notesV2, err := contracts.Evolve(notesV1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.State("editor", "notes", exArr(exObj(exProp("text", exStr()), exProp("tag", exStr())))).
			Method("add", exObj(exProp("text", exStr()), exProp("tag", exStr())), exObj(exProp("count", exInt())))
	})
	must(t, err)
	list := exDefineState("example.list", documents.StateOptions{
		Schema:  exObj(exProp("notes", exArr(exObj(exProp("text", exStr()), exProp("tag", exStr()))))),
		Initial: func() *delta.JsonObject { return jsonObject("notes", []any{}) },
	})
	push := func(draft *delta.Object, text, tag any) {
		_, _ = draft.Array("notes").Push(jsonObject("text", text, "tag", tag))
	}
	count := func(doc *singletonDoc) float64 { return float64(len(doc.Value().Value("notes").([]any))) }
	// The server's notes plugin at v2: what a v1 consumer sees is the provider's `accepts[1]`.
	serveV2 := func(h FacetHost, _ FacetSelf) error {
		accepts := contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
			VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{{Name: "editor", Value: []contracts.Entry[contracts.StateMapping]{{Name: "notes", Value: contracts.StateMapping{
				Func: func(current delta.JsonValue) (delta.JsonValue, error) {
					texts := []any{}
					for _, note := range current.([]any) {
						texts = append(texts, note.(*delta.JsonObject).Value("text"))
					}
					return texts, nil
				},
			}}}}},
			Methods: []contracts.Entry[contracts.MethodMapping]{{Name: "add", Value: contracts.MethodMapping{
				Args: func(old delta.JsonValue) (delta.JsonValue, error) {
					return jsonObject("text", old.(*delta.JsonObject).Value("text"), "tag", "untagged"), nil
				},
				Result: func(current delta.JsonValue) (delta.JsonValue, error) {
					return current.(*delta.JsonObject).Value("count"), nil
				},
			}}},
		}}}
		return h.Serve(notesV2, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, list)
			return exVariant("editor", map[string]chord.Watchable[delta.JsonValue]{
				"notes": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("notes"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					if err := doc.Change(func(draft *delta.Object) { push(draft, args.Value("text"), args.Value("tag")) }, ctx); err != nil {
						return nil, err
					}
					return jsonObject("count", count(doc)), nil
				}),
			}), nil
		}, ServeOptions{Accepts: accepts})
	}
	// The server's notes plugin at v1.
	serveV1 := func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notesV1, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, list)
			return exVariant("editor", map[string]chord.Watchable[delta.JsonValue]{
				"notes": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					texts := []any{}
					for _, note := range values[0].(*delta.JsonObject).Value("notes").([]any) {
						texts = append(texts, note.(*delta.JsonObject).Value("text"))
					}
					return texts, nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					if err := doc.Change(func(draft *delta.Object) { push(draft, args.Value("text"), "") }, ctx); err != nil {
						return nil, err
					}
					return count(doc), nil
				}),
			}), nil
		}, ServeOptions{})
	}
	// 1. A server on v1 and a tab built against v1, connected.
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: noopOnError})
	mustLoad(t)(server.Load([]HostFacet{{Name: "@example/notes", Facet: DefineFacet(serveV1)}}, background))
	tab := newHost(t, HostOptions{OnError: noopOnError})
	mustLoad(t)(tab.Load(nil, background))
	up, down := link.MemoryLinkPair()
	must(t, server.Accept(up, LinkOptions{Principal: ownerPrincipal()}, background))
	must(t, tab.Connect(down, LinkOptions{Principal: serverPrincipal()}, background))
	old := exGet(t, tab, notesV1, jsonObject())
	exCall(t, old, "add", jsonObject("text", "milk"))
	exEqual(t, "1. v1 everywhere", exValue(t, old, "notes"), exJSON(t, `["milk"]`))
	tabWatch, err := old.Member("notes").Watch(background)
	must(t, err)
	tabFrames := nexttest.ChangesOf(tabWatch)
	// 2. The server reloads to v2 with `accepts[1]`: the old tab's hold is kept; its version is an older one now, so it is
	// bound again through the mapping (one snapshot), and from then on gets the mapped diffs.
	_, err = server.Reload([]HostFacet{{Name: "@example/notes", Facet: DefineFacet(serveV2)}}, background, ReloadOptions{})
	must(t, err)
	exWaitState(t, old, "live")
	exEqual(t, "2. the old tab still reads", exValue(t, old, "notes"), exJSON(t, `["milk"]`))
	// 3. The old tab calls v1's `add`: its args are mapped forwards, the v2 result mapped back to v1's count.
	exEqual(t, "3. old add returned", exCall(t, old, "add", jsonObject("text", "eggs")), float64(2))
	scopeEventually(t, "the old tab's mapped diff", func() bool {
		return reflect.DeepEqual(exText(exValue(t, old, "notes")), `["milk","eggs"]`)
	})
	// "tab frame:" of the example: the restart's fresh bound carries the same mapped list, so the tab's watch gets only the
	// mapped diff, "diffs, never the whole list" (Pi prints exactly this one ops list).
	scopeEventually(t, "the tab's frame", func() bool { return len(tabFrames.Items()) >= 1 })
	turn(tab)
	tabCalls := tabFrames.Items()
	if len(tabCalls) != 1 {
		t.Fatalf("tab calls = %+v, want one value call", tabCalls)
	}
	tabOps := make([]nexttest.Frame[delta.JsonValue], len(tabCalls))
	for i, call := range tabCalls {
		if call.Kind != chord.WatchCallValue || call.Retired || call.Error != nil {
			t.Fatalf("tab call %d = %+v, want a value call, not retired", i, call)
		}
		tabOps[i] = nexttest.Frame[delta.JsonValue]{Value: call.Value, Ops: call.Ops}
	}
	wantOps := []any{
		[]any{[]any{"p", []any{}, 1.0, 0.0, []any{"eggs"}}},
	}
	if !nexttest.JSONEqual(opsOf(tabOps), wantOps) {
		t.Fatalf("tab frame ops = %s, want %s", exText(opsOf(tabOps)), exText(wantOps))
	}
	// A new tab (v2 code) on the same object sees the tags.
	fresh := exGet(t, tab, notesV2, jsonObject())
	scopeEventually(t, "a v2 consumer's read", func() bool {
		return exText(exValue(t, fresh, "notes")) == `[{"text":"milk","tag":""},{"text":"eggs","tag":"untagged"}]`
	})
	// 4. Jobs: `Export` v2 maps a v1 run's args and result forwards; a v1 run still running finishes on its old code.
	exportV1, err := DefineJob("example.export", JobOptions{Version: 1, Args: exObj(exProp("path", exStr())), Result: exStr()})
	must(t, err)
	exportV2, err := DefineJob("example.export", JobOptions{
		Version: 2, Args: exObj(exProp("path", exStr()), exProp("format", exStr())), Result: exObj(exProp("file", exStr())),
		Accepts: JobAccepts{1: JobAdapter{
			Args: func(old any) (any, error) {
				return jsonObject("path", old.(*delta.JsonObject).Value("path"), "format", "md"), nil
			},
			Result: func(old any) (any, error) { return jsonObject("file", old), nil },
		}},
	})
	must(t, err)
	exports := exLocal("example.exports", contracts.Params{},
		contracts.Mem("start", contracts.NewMethod(exObj(exProp("path", exStr())), exStr())), contracts.Mem("running", exArr(exUnknown())))
	var finishes seenList[string]
	release := make(chan struct{})
	// The exports plugin with `job` (v1 or v2): its `run` waits for `release` once, its `finish` records the outcome.
	exportsPlugin := func(code string, job *JobDefinition) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(exports, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				handle := self.Job(job, JobImplementation{
					Run: func(args any, ctx JobCtx) (any, error) {
						if _, err := chordctx.Receive(ctx, release); err != nil {
							return nil, err
						}
						path := args.(*delta.JsonObject).Value("path").(string)
						if job.Version == 1 {
							return path + ".md", nil
						}
						return jsonObject("file", path+".out"), nil
					},
					Finish: func(outcome Outcome, _ documents.Tx) error {
						if outcome.Kind == OutcomeFailed {
							finishes.add(fmt.Sprintf("finish on %s: %s args %s error %s/%s", code, outcome.Kind, exText(outcome.Args), outcome.Error.Kind, outcome.Error.Reason))
							return nil
						}
						finishes.add(fmt.Sprintf("finish on %s: %s args %s result %s", code, outcome.Kind, exText(outcome.Args), exText(outcome.Result)))
						return nil
					},
				})
				return exImpl(map[string]chord.Watchable[delta.JsonValue]{"running": Expose(handle.Running())}, map[string]contracts.MethodFunction{
					"start": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						started := jsonObject("path", args.Value("path"))
						if job.Version != 1 {
							started = jsonObject("path", args.Value("path"), "format", "html")
						}
						run, err := handle.Start(started, ctx, StartOptions{})
						if err != nil {
							return nil, err
						}
						return run.ID(), nil
					}),
				}), nil
			}, ServeOptions{})
		})
	}
	worker := newHost(t, HostOptions{OnError: noopOnError})
	mustLoad(t)(worker.Load([]HostFacet{{Name: "@example/exports", Facet: exportsPlugin("v1 code", exportV1)}}, background))
	exportsRef := exGet(t, worker, exports, jsonObject())
	exCall(t, exportsRef, "start", jsonObject("path", "notes"))
	_, err = worker.Reload([]HostFacet{{Name: "@example/exports", Facet: exportsPlugin("v2 code", exportV2)}}, background, ReloadOptions{})
	must(t, err)
	// Pi prints the old attempt's listing of the run here (the v1 run, its args as v1 knew them); whether the restart sealed
	// it already is a race of the loop, so the example's output is asserted from the finish on.
	close(release)
	scopeEventually(t, "running is empty again", func() bool {
		t.Logf("running = %s, finishes = %v, state = %s", exText(exValue(t, exportsRef, "running")), finishes.all(), exStateNow(t, exportsRef))
		return exText(exValue(t, exportsRef, "running")) == "[]"
	})
	// A restart cuts an in-memory run at its seal: its finish runs on the new code, its args mapped forwards once (Pi's
	// example prints the v2 line: `failed`, `aborted` by `reload`).
	scopeEventually(t, "the finish on the new code", func() bool { return len(finishes.all()) >= 1 })
	turn(worker)
	wantFinish := []string{`finish on v2 code: failed args {"path":"notes","format":"md"} error aborted/reload`}
	if got := finishes.all(); !reflect.DeepEqual(got, wantFinish) {
		t.Fatalf("finishes = %q, want %q", got, wantFinish)
	}
	fresh.Release()
	old.Release()
	exportsRef.Release()
	exCloseHost(t, worker)
	exCloseHost(t, tab)
	exCloseHost(t, server)
}
