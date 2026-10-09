package chord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// providerDifferentialServices are package-global service views, so the test defines them once per process.
var providerDifferentialServices = sync.OnceValue(func() map[string]ServiceDefinition[*FacetServiceImplementation] {
	services := map[string]ServiceDefinition[*FacetServiceImplementation]{}
	for _, name := range []string{"s", "k", "x"} {
		services[name] = DefineService[*FacetServiceImplementation]("pud." + name)
	}
	return services
})

type providerDifferentialOp struct {
	Op           string      `json:"op"`
	Shape        []string    `json:"shape"`
	Service      string      `json:"service"`
	Key          string      `json:"key"`
	Closer       int         `json:"closer"`
	State        int         `json:"state"`
	Frames       []Ops       `json:"frames"`
	Mode         ServiceMode `json:"mode"`
	Throws       bool        `json:"throws"`
	CloseAfter   int         `json:"closeAfter"`
	Subscription int         `json:"subscription"`
	Call         ServiceCall `json:"call"`
	React        *struct {
		At      int         `json:"at"`
		Action  string      `json:"action"`
		Service string      `json:"service"`
		Mode    ServiceMode `json:"mode"`
		State   int         `json:"state"`
		Frame   Ops         `json:"frame"`
	} `json:"react"`
}

type providerDifferentialObservation struct {
	Result string   `json:"result"`
	Events []string `json:"events"`
}

// describeProviderError renders an error as testdata/provider_update_differential.mjs does: an AggregateError with its causes, a
// RemoteServiceError with its code, and any other error by message.
func describeProviderError(err any) string {
	if aggregate, ok := err.(*AggregateError); ok {
		causes := make([]string, 0, len(aggregate.Errors))
		for _, cause := range aggregate.Errors {
			causes = append(causes, describeProviderError(cause))
		}
		return fmt.Sprintf("agg %s [%s]", aggregate.Message, strings.Join(causes, "; "))
	}
	if cause, ok := err.(error); ok {
		if remote, ok := errors.AsType[*RemoteServiceError](cause); ok {
			return string(remote.Code) + " " + remote.Message
		}
		return "- " + cause.Error()
	}
	return fmt.Sprintf("- %v", err)
}

// Pi packages/chord/src/services/provider.ts:113-212 (provide, withdraw, validateReplacement, replace, use, spawn and its closer),
// 214-237 (invoke), 239-276 (subscribe, activate, close), 278-317 (dispose), 333-365 (#createInstance state listeners), 376-411
// (#resolveInstance), 413-445 (#snapshot, #snapshotInstance), 447-466 (#emit with the 100-update reset), 481-501 (drainSubscriber) and
// 503-557 (snapshot sequence coverage) against the installed chord 1.1.0 dist, by testdata/provider_update_differential.mjs. Seeded
// random sequences of provider operations run on a singleton service pud.s and a keyed service pud.k, with pud.x outside the catalogue.
// Implementations carry the oracle's member shapes, with attached states (state.ts:324-341) on the Pi side and FacetState on the Go side
// fed the same source frames. From inside its listener a subscriber may throw, close itself, publish a state frame (also to the state being delivered, so the frame queues behind the running delivery), open and activate a nested subscription after such a publication (the snapshot-sequence coverage case), withdraw the singleton or dispose the provider. Each operation's result (an error
// rendered with its code or aggregated causes) and the updates every listener received during it, plus the listener failures a state
// publication reports, must equal Pi's, with JSON object keys compared in their order. Go FacetState.Apply returns the failures that Pi's attached
// state reports to its onError, so both are recorded as that state's event.
func TestRemoteServiceProviderOperationsMatchPiOnRandomSequences(t *testing.T) {
	const scenarios, steps, seed = 200, 30, 20260705
	cmd := exec.CommandContext(t.Context(), "node", "testdata/provider_update_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(scenarios), strconv.Itoa(steps), strconv.Itoa(seed), "pud")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var cases []struct {
		Ops      []json.RawMessage                 `json:"ops"`
		Observed []providerDifferentialObservation `json:"observed"`
	}
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != scenarios {
		t.Fatalf("Pi oracle returned %d scenarios, want %d", len(cases), scenarios)
	}
	services := providerDifferentialServices()
	canonical := func(line string) string {
		if start := strings.Index(line, "{"); start > 0 && !strings.HasPrefix(line, "!") {
			return line[:start] + canonicalJSON(t, []byte(line[start:]))
		}
		if strings.HasPrefix(line, `"`) {
			return canonicalJSON(t, []byte(line))
		}
		return line
	}
	failures := 0
	for index, c := range cases {
		provider, err := NewRemoteServiceProvider(SingletonService(services["s"]), KeyedService(services["k"]))
		if err != nil {
			t.Fatal(err)
		}
		var (
			mu              sync.Mutex
			events          []string
			states          []*FacetState
			cursors         []int
			implementations []*FacetServiceImplementation
			closers         []func() error
			subscriptions   []ServiceSubscription
		)
		record := func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}
		newImplementation := func(shape []string) *FacetServiceImplementation {
			id := len(implementations)
			implementation := &FacetServiceImplementation{}
			for _, name := range shape {
				if name == "m" {
					implementation.Members = append(implementation.Members, FacetServiceMember{Name: "m", Invoke: func(_ context.Context, args []json.RawMessage) (json.RawMessage, error) {
						texts := make([]string, 0, len(args))
						for _, arg := range args {
							var value any
							if err := json.Unmarshal(arg, &value); err != nil {
								return nil, err
							}
							texts = append(texts, fmt.Sprint(value))
						}
						return json.Marshal(fmt.Sprintf("i%d.m(%d:%s)", id, len(args), strings.Join(texts, ",")))
					}})
					continue
				}
				state, err := NewFacetState(map[string]any{"a": float64(len(states))}, 0)
				if err != nil {
					t.Fatal(err)
				}
				states = append(states, state)
				cursors = append(cursors, 0)
				implementation.Members = append(implementation.Members, FacetServiceMember{Name: name, State: state})
			}
			implementations = append(implementations, implementation)
			return implementation
		}
		result := func(err error) string {
			if err != nil {
				return "!" + describeProviderError(err)
			}
			return "ok"
		}
		// subscribe opens subscription index the way the oracle's subscribe does; a nested subscription (top false) is activated at
		// once and has no listener behaviour of its own.
		var subscribe func(index int, service string, mode ServiceMode, spec providerDifferentialOp, top bool) (string, error)
		subscribe = func(index int, service string, mode ServiceMode, spec providerDifferentialOp, top bool) (string, error) {
			received := 0
			subscription, err := provider.Subscribe(services[service].Id(), mode, func(_ context.Context, update ServiceProviderUpdate) {
				received++
				encoded, err := json.Marshal(update)
				if err != nil {
					panic(err)
				}
				record(fmt.Sprintf("sub%d %s", index, encoded))
				if spec.CloseAfter == received {
					if err := subscriptions[index].Close(context.Background()); err != nil {
						panic(err)
					}
				}
				if react := spec.React; react != nil && react.At == received {
					if react.Action == "publish" || react.Action == "publishSubscribe" {
						cursors[react.State]++
						if err := states[react.State].Apply(context.Background(), cursors[react.State], react.Frame); err != nil {
							record(fmt.Sprintf("state%d %s", react.State, describeProviderError(err)))
						}
					}
					switch react.Action {
					case "withdraw":
						record(fmt.Sprintf("sub%d withdraws %s", index, result(Withdraw(provider, services["s"]))))
					case "dispose":
						record(fmt.Sprintf("sub%d disposes %s", index, result(provider.Dispose())))
					case "subscribe", "publishSubscribe":
						nested := len(subscriptions)
						subscriptions = append(subscriptions, nil)
						opened, err := subscribe(nested, react.Service, react.Mode, providerDifferentialOp{}, false)
						if err != nil {
							opened = "!" + describeProviderError(err)
						}
						record(fmt.Sprintf("sub%d opens %s", index, opened))
					}
				}
				if spec.Throws {
					panic(fmt.Errorf("listener %d failed on %s", index, update.Type))
				}
			})
			if err != nil {
				return "", err
			}
			subscriptions[index] = subscription
			snapshot, err := json.Marshal(subscription.Snapshot())
			if err != nil {
				return "", err
			}
			if !top {
				if err := subscription.Activate(); err != nil {
					return "", err
				}
			}
			return fmt.Sprintf("sub%d %s", index, snapshot), nil
		}
		var trace []string
		mismatch := false
		for step, raw := range c.Ops {
			var op providerDifferentialOp
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("scenario %d step %d: decode %s: %v", index, step, raw, err)
			}
			mu.Lock()
			events = []string{}
			mu.Unlock()
			var got string
			switch op.Op {
			case "provide":
				got = result(Provide(provider, services["s"], newImplementation(op.Shape)))
			case "withdraw":
				got = result(Withdraw(provider, services["s"]))
			case "replace":
				got = result(Replace(provider, services["s"], newImplementation(op.Shape)))
			case "validate":
				got = result(ValidateReplacement(provider, services["s"], newImplementation(op.Shape)))
			case "use":
				implementation, err := Use(provider, services[op.Service])
				got = result(err)
				if err == nil {
					got = fmt.Sprintf("i%d", slices.Index(implementations, implementation))
				}
			case "spawn":
				closeInstance, err := Spawn(provider, services[op.Service], op.Key, newImplementation(op.Shape))
				got = result(err)
				if err == nil {
					closers = append(closers, closeInstance)
					got = fmt.Sprintf("closer%d", len(closers)-1)
				}
			case "close":
				got = result(closers[op.Closer]())
			case "publish":
				for _, frame := range op.Frames {
					cursors[op.State]++
					if err := states[op.State].Apply(context.Background(), cursors[op.State], frame); err != nil {
						record(fmt.Sprintf("state%d %s", op.State, describeProviderError(err)))
					}
				}
				got = "ok"
			case "subscribe":
				subscriber := len(subscriptions)
				subscriptions = append(subscriptions, nil)
				var err error
				got, err = subscribe(subscriber, op.Service, op.Mode, op, true)
				if err != nil {
					got = result(err)
				}
			case "activate":
				got = "none"
				if subscription := subscriptions[op.Subscription]; subscription != nil {
					got = result(subscription.Activate())
				}
			case "unsubscribe":
				got = "none"
				if subscription := subscriptions[op.Subscription]; subscription != nil {
					got = result(subscription.Close(context.Background()))
				}
			case "invoke":
				value, err := provider.Invoke(context.Background(), op.Call)
				got = result(err)
				if err == nil {
					got = string(value)
				}
			case "dispose":
				got = result(provider.Dispose())
			default:
				t.Fatalf("scenario %d step %d: unknown op %s", index, step, op.Op)
			}
			mu.Lock()
			gotObservation := providerDifferentialObservation{Result: canonical(got), Events: make([]string, 0, len(events))}
			for _, event := range events {
				gotObservation.Events = append(gotObservation.Events, canonical(event))
			}
			mu.Unlock()
			want := c.Observed[step]
			want.Result = canonical(want.Result)
			for i, event := range want.Events {
				want.Events[i] = canonical(event)
			}
			gotJSON, _ := json.Marshal(gotObservation)
			wantJSON, _ := json.Marshal(want)
			trace = append(trace, fmt.Sprintf("%s\n      got  %s\n      want %s", raw, gotJSON, wantJSON))
			if string(gotJSON) != string(wantJSON) {
				mismatch = true
				break
			}
		}
		if mismatch {
			failures++
			if failures <= 3 {
				t.Errorf("scenario %d differs from Pi:\n    %s", index, strings.Join(trace[max(0, len(trace)-4):], "\n    "))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, scenarios)
	}
}
