package host

import (
	"context"
	"errors"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Own test (no Pi file; D430): the awaits of Pi's handler resume inside the event loop. A Go handler that suspends (it
// waits on its own goroutine) resumes with Resume, whose work reads the host's state as a section of the loop; from the
// handler's admitting section it runs inline; a ctx that is no handler's is refused.

var resumeContract = mustContract(contracts.Local("app.resume", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("probe", contracts.NewMethod(contracts.Int()))},
}))

func TestResumeRunsAfterAHandlerSuspendedInASectionOfTheLoop(t *testing.T) {
	h := newHost(t, HostOptions{OnError: func(err HostError) { t.Errorf("onError %s", err.Kind) }})
	loop := h.(*chordHost).events.(*Loop)
	mustLoad(t)(h.Load([]HostFacet{{Name: "@test/resume", Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
		return fh.Serve(resumeContract, func(_ *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{
					"probe": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						var inline bool
						if err := Resume(ctx, func() { inline = loop.onLoop() }); err != nil {
							return nil, err
						}
						var suspended bool
						errs := make(chan error, 1)
						go func() {
							// A goroutine of its own, as a handler's blocking work leaves the section.
							if loop.onLoop() {
								errs <- context.Canceled
								return
							}
							errs <- Resume(ctx, func() { suspended = loop.onLoop() })
						}()
						// The handler suspends (it leaves its admitting section) while it waits, as Pi's await does.
						if err, rerr := chordctx.Receive(ctx, errs); rerr != nil || err != nil {
							return nil, errors.Join(err, rerr)
						}
						// 1: the work ran on the loop inline, 2: after a suspension.
						answer := 0.0
						if inline {
							answer++
						}
						if suspended {
							answer += 2
						}
						return answer, nil
					},
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	object, err := h.Get(background, resumeContract, jsonObject(), GetOptions{})
	must(t, err)
	got, err := object.Call("probe", 0.0, background)
	must(t, err)
	if got != 3.0 {
		t.Fatalf("probe = %v, want 3 (the work ran on the loop inline and after a suspension)", got)
	}
}

func TestResumeRefusesACtxThatIsNoHandlers(t *testing.T) {
	if err := Resume(context.Background(), func() { t.Error("work ran") }); err == nil {
		t.Fatal("Resume(background) succeeded, want an error")
	}
}
