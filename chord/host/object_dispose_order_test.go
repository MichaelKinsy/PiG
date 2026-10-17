package host

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// object.ts:1186-1205 call, with #invoke (:1213-1222): the invocation's settle handler (which removes it and runs
// #maybeIdle) is attached before call awaits it, so a callee that lost its last hold during the call starts its
// disposal before the call's finally releases the holds its ctx made. Each disposal then closes its resources in that
// order (object.ts:1260-1300: await the invocations, then runAll the closes).
func TestObjectCallDisposesTheIdleCalleeBeforeReleasingTheCallsRefs(t *testing.T) {
	var mu sync.Mutex
	var log []string
	push := func(entry string) {
		mu.Lock()
		log = append(log, entry)
		mu.Unlock()
	}
	logged := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(log)
	}
	held := Local("t.held", LocalDef{Version: 1, Input: tObject()})
	callee := Local("t.callee", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"run", Method(nil, nil)}}})
	withResource := func(name string) func(self Self) error {
		return func(self Self) error {
			_, err := self.Resource("r", ResourceDeclaration[any]{
				Version: 1,
				Open:    func(ResourceOpenOptions) (any, error) { return nil, nil },
				Close:   func(any) error { push("close " + name); return nil },
			})
			return err
		}
	}
	host := requestsHost(t, HostOptions{})
	requestsServe(t, host.Serve, held, func(_ any, _ Principal, self Self) (Implementation, error) {
		return chordjson.NewObject(0), withResource("held")(self)
	}, ServeOptions{})
	var calleeRef Ref
	requestsServe(t, host.Serve, callee, func(_ any, _ Principal, self Self) (Implementation, error) {
		if err := withResource("callee")(self); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("run", methodFunc(func(_ any, ctx Ctx) (any, error) {
			// A hold the call's ctx makes, released when the call settles.
			ref, err := ctx.Get(held, requestsEmpty(), nil)
			if err != nil {
				return nil, err
			}
			if err := readyErr(t, ref); err != nil {
				return nil, err
			}
			// The callee's last hold goes away while the call runs: it is idle once the call settles.
			calleeRef.Release()
			return nil, nil
		})), nil
	}, ServeOptions{})
	calleeRef = requestsGet(t, host, callee, requestsEmpty(), nil)
	if err := readyErr(t, calleeRef); err != nil {
		t.Fatal(err)
	}
	run := calleeRef.Member("run").(methodFunc)
	if _, err := run(nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both disposals", func() bool { return len(logged()) == 2 })
	if got := logged(); !slices.Equal(got, []string{"close callee", "close held"}) {
		t.Fatalf("closes = %v", got)
	}
}
