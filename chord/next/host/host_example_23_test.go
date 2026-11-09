package host

// pi: packages/chord/test/examples/23-keyed-watches.ts

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// --- 23-keyed-watches.ts -------------------------------------------------------------------------------------------

func TestExample23KeyedWatches(t *testing.T) {
	notesDef := exDefineState("notes", documents.StateOptions{
		Schema:  exObj(exProp("body", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("body", "") },
		Keyed:   true,
	})
	notebook := exLocal("example.notebook", contracts.Params{},
		// The note "pinned" as a state member: `initial()` while absent.
		contracts.Mem("pinned", exObj(exProp("body", exStr()))),
		contracts.Mem("write", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("body", exStr())))),
		contracts.Mem("remove", contracts.NewMethod(exStr())))
	// The example's own window into the object: its keyed handle, to watch other keys from outside.
	var notes *documents.Keyed
	host, _ := exOpenHost(t, loadedOptions{}, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notebook, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			handle := self.State(notesDef).(*documents.Keyed)
			notes = handle
			pinned, err := handle.At("pinned")
			if err != nil {
				return contracts.Implementation{}, err
			}
			// Pinned while this attempt lives, and loaded before the object goes live.
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"pinned": Expose(pinned)}, map[string]contracts.MethodFunction{
				"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					_, err := handle.Change(ctx, exParam(args, "id"), func(draft *delta.Object) (any, error) {
						return nil, draft.Set("body", args.Value("body"))
					})
					return nil, err
				}),
				"remove": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, handle.Retire(ctx, id.(string))
				},
			}), nil
		}, ServeOptions{})
	})
	ref := exGet(t, host, notebook, jsonObject())
	// A watch of the state member: every value call carries the value, its ops and the retired flag.
	_, pinnedFrames := exWatch(t, background, ref.Member("pinned"))
	exCall(t, ref, "write", jsonObject("id", "pinned", "body", "buy milk"))
	// A watch of one key; an absent key gives `undefined` (watch it once it exists, or pin it with `at`).
	absent, err := notes.Watch(background, "later")
	must(t, err)
	if absent != nil {
		t.Fatalf("watch of an absent key = %v, want none", absent)
	}
	exCall(t, ref, "write", jsonObject("id", "later", "body", "call Anna"))
	laterWatch, err := notes.Watch(background, "later")
	must(t, err)
	if laterWatch == nil {
		t.Fatal("watch of a present key: none")
	}
	var laterFrames exFrames
	laterWatch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		laterFrames.mu.Lock()
		defer laterFrames.mu.Unlock()
		laterFrames.frames = append(laterFrames.frames, exFrame{Kind: string(call.Kind), Value: call.Value, Retired: call.Retired})
		return nil
	})
	exCall(t, ref, "remove", "later")
	exCall(t, ref, "remove", "pinned")
	// Frames are delivered after the commit, asynchronously.
	scopeEventually(t, "the retirements' frames", func() bool { return len(pinnedFrames.all()) == 2 && len(laterFrames.all()) >= 1 })
	pinned := pinnedFrames.all()
	exEqual(t, "pinned", pinned[0].Value, exJSON(t, `{"body":"buy milk"}`))
	if pinned[0].Retired || !pinned[1].Retired {
		t.Fatalf("pinned retired flags = %v, %v", pinned[0].Retired, pinned[1].Retired)
	}
	exEqual(t, "pinned after the retirement", pinned[1].Value, exJSON(t, `{"body":""}`))
	later := laterFrames.all()
	if last := later[len(later)-1]; !last.Retired {
		t.Fatalf("later: last frame not retired: %+v", later)
	}
	exCloseHost(t, host)
}
