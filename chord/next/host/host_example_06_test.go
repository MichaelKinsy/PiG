package host

// pi: packages/chord/test/examples/06-natives.ts

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// --- 06-natives.ts -------------------------------------------------------------------------------------------------

// hungClose is what a close that never settles (Pi's promise that nothing resolves) waits on: TestMain closes it after the
// tests, so the goroutine of that close is not one the leak check reports.
var hungClose = make(chan struct{})

func TestExample06Natives(t *testing.T) {
	clock := exLocal("example.clock", contracts.Params{exProp("zone", exStr())},
		contracts.Mem("zone", exStr()), contracts.Mem("now", contracts.NewMethod(contracts.Schema{}, exStr())))
	leaky := exLocal("example.leaky", contracts.Params{}, contracts.Mem("n", exInt()))
	var logs exCollector
	var ticks sync.Map
	var reported exCollector
	var reportedErrors []HostError
	var reportedMu sync.Mutex
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		reportedMu.Lock()
		reportedErrors = append(reportedErrors, err)
		reportedMu.Unlock()
		reported.add(string(err.Kind) + " " + err.Object)
	}})
	_ = errs
	mustLoad(t)(host.Load([]HostFacet{{Name: "@example/natives", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(clock, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			zone := exParam(params, "zone")
			// `self.open(open, close)` declares a native; it opens after the references are live, in declaration order.
			config := self.Open(func(context.Context) (any, error) {
				logs.add(zone + ": open config")
				return "[" + zone + "]", nil
			}, func(_ any, reason NativeCloseReason, _ context.Context) error {
				logs.add(fmt.Sprintf("%s: close config (%s)", zone, reason.Kind))
				return nil
			})
			ticker := self.Open(func(context.Context) (any, error) {
				prefix, _ := config.Value()
				logs.add(fmt.Sprintf("%s: open ticker, config %v", zone, prefix))
				counter := &exTicker{}
				ticks.Store(zone, counter)
				counter.start()
				return counter, nil
			}, func(value any, reason NativeCloseReason, _ context.Context) error {
				value.(*exTicker).stop()
				logs.add(fmt.Sprintf("%s: close ticker (%s)", zone, reason.Kind))
				return nil
			})
			// The factory only declares: a value is there once its open resolved.
			if _, err := config.Value(); err != nil {
				chordErr := exCode(t, err, data.CodeNotReady, "")
				logs.add(zone + ": in the factory, " + chordErr.Message)
			}
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"zone": exConstant(zone)}, map[string]contracts.MethodFunction{
				"now": exMethod(func(*delta.JsonObject, context.Context) (delta.JsonValue, error) {
					prefix, err := config.Value()
					if err != nil {
						return nil, err
					}
					counter, err := ticker.Value()
					if err != nil {
						return nil, err
					}
					if counter.(*exTicker).count() > 0 {
						return fmt.Sprintf("%v ticking", prefix), nil
					}
					return fmt.Sprintf("%v starting", prefix), nil
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		// Closes that throw or hang: they never stop the other closes.
		return h.Serve(leaky, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return "socket", nil }, func(any, NativeCloseReason, context.Context) error {
				return errors.New("socket close failed")
			})
			closeTimeout := 50 * time.Millisecond
			self.Open(func(context.Context) (any, error) { return "worker", nil }, func(any, NativeCloseReason, context.Context) error {
				<-hungClose // never settles
				return nil
			}, NativeOptions{CloseTimeout: &closeTimeout})
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"n": exConstant(float64(1))}, nil), nil
		}, ServeOptions{})
	})}}, background))
	vienna := exGet(t, host, clock, jsonObject("zone", "vienna"))
	exEqual(t, "now", exCall(t, vienna, "now", nil), "[vienna] starting")
	scopeEventually(t, "the ticker ticks", func() bool {
		result, err := vienna.Call("now", nil, background)
		return err == nil && result == "[vienna] ticking"
	})
	vienna.Release()
	scopeEventually(t, "vienna's natives closed", func() bool {
		all := strings.Join(logs.all(), "\n")
		return strings.Contains(all, "vienna: close ticker (end)") && strings.Contains(all, "vienna: close config (end)")
	})
	exGet(t, host, clock, jsonObject("zone", "tokyo"))
	err := host.Close(background)
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 1 || joined.Unwrap()[0].Error() != "socket close failed" {
		t.Fatalf("host.close = %v, want an aggregate of [socket close failed]", err)
	}
	all := logs.all()
	wantStart := []string{
		"vienna: in the factory, " + all[0][len("vienna: in the factory, "):], "vienna: open config", "vienna: open ticker, config [vienna]",
	}
	if len(all) < 3 || !strings.HasPrefix(all[0], "vienna: in the factory, ") || !reflect.DeepEqual(all[1:3], wantStart[1:]) {
		t.Fatalf("vienna's start = %v", all)
	}
	joinedLogs := strings.Join(all, "\n")
	for _, line := range []string{"tokyo: close ticker (close)", "tokyo: close config (close)"} {
		if !strings.Contains(joinedLogs, line) {
			t.Fatalf("missing %q in %v", line, all)
		}
	}
	got := reported.all()
	// A close that threw is returned by `host.close` (the aggregate above), not reported; only the timeout is.
	if !reflect.DeepEqual(got, []string{"close_timeout example.leaky"}) {
		t.Fatalf("reported = %v", got)
	}
}

// exTicker is Pi's `{ ticks, timer: setInterval(..., 10) }`.
type exTicker struct {
	mu    sync.Mutex
	ticks int
	timer *time.Ticker
	done  chan struct{}
}

func (e *exTicker) start() {
	e.timer = time.NewTicker(10 * time.Millisecond)
	e.done = make(chan struct{})
	go func() {
		for {
			select {
			case <-e.timer.C:
				e.mu.Lock()
				e.ticks++
				e.mu.Unlock()
			case <-e.done:
				return
			}
		}
	}()
}

func (e *exTicker) stop() {
	e.timer.Stop()
	close(e.done)
}

func (e *exTicker) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ticks
}
