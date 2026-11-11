package host

// pi: packages/chord/test/next/examples/05-errors.ts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// --- 05-errors.ts --------------------------------------------------------------------------------------------------

func TestExample05Errors(t *testing.T) {
	echo := exLocal("example.echo", contracts.Params{exProp("id", exInt())},
		contracts.Mem("id", exInt()), contracts.Mem("echo", contracts.NewMethod(exObj(exProp("text", exStr())), exStr())))
	ping := exLocal("example.ping", contracts.Params{}, contracts.Mem("n", exInt()))
	pong := exLocal("example.pong", contracts.Params{}, contracts.Mem("n", exInt()))
	broken := exLocal("example.broken", contracts.Params{exProp("id", exInt())}, contracts.Mem("n", exInt()))
	sloppy := exLocal("example.sloppy", contracts.Params{exProp("id", exInt())}, contracts.Mem("n", exInt()))
	var reported exCollector
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		errs.push(err)
		if err.Kind == ErrActivationFailed {
			reported.add(fmt.Sprintf("reported: %s failed %s", err.Object, err.Failure.Kind))
		} else {
			reported.add("reported: " + string(err.Kind))
		}
	}})
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(echo, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"id": exConstant(params.Value("id"))}, map[string]contracts.MethodFunction{
				"echo": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					return args.Value("text"), nil
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(broken, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, errors.New("broken: missing configuration")
		}, ServeOptions{}); err != nil {
			return err
		}
		// Chord checks the implementation's members when the object activates.
		return h.Serve(sloppy, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": exConstant(float64(1)), "extra": exConstant(float64(2))}, nil), nil
		}, ServeOptions{})
	})
	// Ping and Pong are served by a second plugin: types with empty params activate at load.
	cycle := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(ping, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(pong, jsonObject()).Member("n"))}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(pong, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(ping, jsonObject()).Member("n"))}, nil), nil
		}, ServeOptions{})
	})
	// A dependency cycle fails every object on it broken, with invalid/cycle; load still resolves.
	mustLoad(t)(host.Load([]HostFacet{{Name: "@example/errors", Facet: facet}, {Name: "@example/cycle", Facet: cycle}}, background))
	if got := reported.all(); len(got) == 0 || got[0] != "reported: example.ping failed broken" && got[0] != "reported: example.pong failed broken" {
		t.Fatalf("cycle reports = %v", got)
	}
	_, err := host.Get(background, ping, jsonObject(), GetOptions{})
	_ = exCode(t, err, data.CodeInvalid, data.ReasonCycle)
	echoRef := exGet(t, host, echo, jsonObject("id", float64(1)))
	// Arguments are checked against the contract at the object: wrong types and unknown fields are invalid/args.
	_, err = echoRef.Call("echo", jsonObject("text", float64(1)), background)
	_ = exCode(t, err, data.CodeInvalid, data.ReasonArgs)
	_, err = echoRef.Call("echo", jsonObject("text", "hi", "extra", true), background)
	_ = exCode(t, err, data.CodeInvalid, data.ReasonArgs)
	// Params select the object and are checked on every get: invalid/params.
	_, err = host.Get(background, echo, jsonObject("id", "one"), GetOptions{})
	_ = exCode(t, err, data.CodeInvalid, data.ReasonParams)
	// An implementation with a member its contract does not declare fails activation.
	_, err = host.Get(background, sloppy, jsonObject("id", float64(1)), GetOptions{})
	if err == nil {
		t.Fatal("unknown member: ok")
	}
	// A get needs the provider's version and fingerprint; another declaration does not fit: contract_mismatch after readyTimeout.
	echoV2 := mustContract(contracts.Local("example.echo", contracts.LocalDeclaration{
		Version: 2, Params: contracts.Params{exProp("id", exInt())}, Members: []contracts.LocalEntry{contracts.Mem("id", exInt())},
	}))
	readyTimeout := 100 * time.Millisecond
	_, err = host.Get(background, echoV2, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: &readyTimeout})
	_ = exCode(t, err, data.CodeContractMismatch, "")
	// A factory that throws fails broken: the get rejects at once with its error, and no object is left behind.
	_, err = host.Get(background, broken, jsonObject("id", float64(1)), GetOptions{})
	if err == nil || !strings.Contains(err.Error(), "broken: missing configuration") {
		t.Fatalf("throwing factory: %v", err)
	}
	// After release, every use of a reference throws released.
	echoRef.Release()
	_, err = echoRef.Call("echo", jsonObject("text", "hi"), background)
	_ = exCode(t, err, data.CodeReleased, "")
	exCloseHost(t, host)
	_, err = host.Get(background, echo, jsonObject("id", float64(1)), GetOptions{})
	_ = exCode(t, err, data.CodeReleased, "")
}

// exNumber is a stored integer as the JSON number Pi's row has.
func exNumber(value any) any {
	if number, ok := value.(int64); ok {
		return float64(number)
	}
	return value
}

// exLoop is the loop of the host a handler's ctx belongs to: what `Awaiting` takes.
func exLoop(ctx Ctx) sectionLoop { return ctx.(*callContext).host.loop() }

// exCtx is the handler's `ctx` as Chord's Ctx.
func exCtx(ctx context.Context) Ctx {
	handler, ok := ctx.(Ctx)
	if !ok {
		panic(fmt.Sprintf("a handler's context is a Ctx, got %T", ctx))
	}
	return handler
}

// exBlocked is a method function that waits until its context is aborted, and then reports why through onAbort.
func exBlocked(onAbort func(cause error)) contracts.MethodFunction {
	return func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
		// Pi: `await untilAborted(ctx)`.
		UntilAborted(ctx)
		if onAbort != nil {
			onAbort(context.Cause(ctx))
		}
		return nil, nil
	}
}
