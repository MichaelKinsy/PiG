package host

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// pi: packages/chord/src/state/derive.ts
// Mirrors cases of packages/chord/test/state/native-source.test.ts and derive.test.ts that run on a host.

// Mirrors "native sources as derive sources › `ref.state` is a derive source: a state member can show a dependency's reachability".
func TestNativeSourcesAsDeriveSourcesRefStateIsADeriveSourceAStateMemberCanShowADependencysReachability(t *testing.T) {
	flaky := mustContract(contracts.Local("app.flaky", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{}}))
	watcher := exLocal("app.watcher", contracts.Params{}, contracts.Mem("flaky", contracts.Str()))
	var flakySelf Self
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(flaky, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			flakySelf = self
			return exImpl(nil, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(watcher, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			dependency := self.Get(flaky, jsonObject("id", 1.0))
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"flaky": self.Derive([]any{dependency.State()}, func(values []any) (delta.JsonValue, error) {
					return string(values[0].(*RefState).Kind), nil
				}),
			}, nil), nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref := mustGet(t, host, watcher, jsonObject())
	var mu sync.Mutex
	var kinds []string
	watch, err := ref.Member("flaky").Watch(background)
	must(t, err)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			mu.Lock()
			kinds = append(kinds, call.Value.(string))
			mu.Unlock()
		}
		return nil
	})
	if got, _ := ref.Member("flaky").Value(); got != "live" {
		t.Fatalf("flaky = %v, want live", got)
	}
	failLive(flakySelf, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "down"}, nil))
	if got, _ := ref.Member("flaky").Value(); got != "failed" {
		t.Fatalf("flaky = %v, want failed", got)
	}
	deliveredOn(host)
	mu.Lock()
	got := append([]string(nil), kinds...)
	mu.Unlock()
	if want := []string{"failed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	must(t, host.Close(background))
}

// Mirrors "GC of handed-out errors › a failure call's fresh error kept by its listener keeps nothing of its host once the host closes".
func TestGCOfHandedOutErrorsAFailureCallsFreshErrorKeptByItsListenerKeepsNothingOfItsHostOnceTheHostCloses(t *testing.T) {
	num, err := documents.DefineState("app.num", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	must(t, err)
	fresh := exLocal("app.fresh", contracts.Params{},
		contracts.Mem("t", contracts.Int()), contracts.Mem("set", contracts.NewMethod(contracts.Int())))
	var (
		attemptRef weak.Pointer[attempt]
		hostRef    weak.Pointer[chordHost]
		kept       []error
		mu         sync.Mutex
	)
	func() {
		host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
			return h.Serve(fresh, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				attemptRef = weak.Make(self.(*attempt))
				document := stateOf(self, num)
				member := self.Derive([]any{document.Member()}, func(values []any) (delta.JsonValue, error) {
					// A fresh error each time.
					if values[0].(*delta.JsonObject).Value("n") == 1.0 {
						return nil, errors.New("fresh")
					}
					return values[0].(*delta.JsonObject).Value("n"), nil
				})
				return exImpl(map[string]chord.Watchable[delta.JsonValue]{"t": member}, map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := self.Tx(func(tx documents.Tx) (any, error) {
							draft, err := tx.Draft(document.s)
							if err != nil {
								return nil, err
							}
							return nil, draft.Set("n", args)
						}, ctx)
						return nil, err
					},
				}), nil
			}, ServeOptions{})
		}, loadedOptions{})
		ref := mustGet(t, host, fresh, jsonObject())
		watch, err := ref.Member("t").Watch(background)
		must(t, err)
		watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
			if call.Kind == chord.WatchCallFailed {
				mu.Lock()
				kept = append(kept, call.Error)
				mu.Unlock()
			}
			return nil
		})
		exCall(t, ref, "set", 1.0)
		deliveredOn(host)
		must(t, host.Close(background))
		hostRef = weak.Make(host.(*chordHost))
	}()
	mu.Lock()
	count := len(kept)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("kept errors = %d, want 1", count)
	}
	expectCollected(t, attemptRef)
	expectCollected(t, hostRef)
}
