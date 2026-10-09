package chord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"

	overlay "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// stateChangeHolder publishes one replicated state as service member s.
type stateChangeHolder struct {
	state *MutableReplicatedState[*chordjson.Object]
}

func (holder *stateChangeHolder) S() *MutableReplicatedState[*chordjson.Object] { return holder.state }

// stateChangeService is a package-global service view, so the test defines it once per process.
var stateChangeService = sync.OnceValue(func() ServiceDefinition[*stateChangeHolder] {
	return DefineService[*stateChangeHolder]("scd.a")
})

// stateChangeEdit is one draft edit of testdata/state_change_differential.mjs: ["set", key, n], ["del", key] or ["nest", n].
type stateChangeEdit []any

func (edit stateChangeEdit) apply(draft *overlay.Object) error {
	switch edit[0] {
	case "set":
		return draft.Set(edit[1].(string), edit[2])
	case "del":
		draft.Delete(edit[1].(string))
		return nil
	case "nest":
		return draft.Object("o").Set("x", edit[1])
	}
	return fmt.Errorf("unknown edit %v", edit)
}

// Pi packages/chord/src/services/state.ts:109-180 (ReplicatedStatePublisher subscribe and the queued publish), 28-107 (StateSubscriber
// delivery) and 184-245 (MutableReplicatedStateImpl change and replace: the reentrancy errors, the aborted draft of a throwing or
// reentrant callback, the unchanged draft that publishes nothing and the root replacement) against the installed chord 1.1.0 dist, by
// testdata/state_change_differential.mjs. Seeded random sequences of draft changes (Go Edit, the draft-handle form), replaces (sometimes
// of an equal value), subscriptions (whose listener may change the state from inside its delivery) and unsubscriptions run on a state
// published by a RemoteServiceProvider as member s. After each step the result, every listener delivery, every provider state update
// and the state's value must equal Pi's, with JSON object keys compared in their order.
func TestMutableReplicatedStateChangesMatchPiOnRandomSequences(t *testing.T) {
	const scenarios, steps, seed = 300, 20, 20260707
	service := stateChangeService()
	cmd := exec.CommandContext(t.Context(), "node", "testdata/state_change_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(scenarios), strconv.Itoa(steps), strconv.Itoa(seed), service.Id())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	type observation struct {
		Result string   `json:"result"`
		Events []string `json:"events"`
		Value  string   `json:"value"`
	}
	var cases []struct {
		Ops []struct {
			Op     string            `json:"op"`
			Edits  []stateChangeEdit `json:"edits"`
			Nested string            `json:"nested"`
			Throws bool              `json:"throws"`
			Value  *chordjson.Object `json:"value"`
			React  *struct {
				At   int             `json:"at"`
				Edit stateChangeEdit `json:"edit"`
			} `json:"react"`
			Index int `json:"index"`
		} `json:"ops"`
		Observed []observation `json:"observed"`
	}
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != scenarios {
		t.Fatalf("Pi oracle returned %d scenarios, want %d", len(cases), scenarios)
	}
	canonical := func(line string) string {
		if start := strings.IndexAny(line, "[{"); start >= 0 && !strings.Contains(line[:start], "!") {
			return line[:start] + canonicalJSON(t, []byte(line[start:]))
		}
		return line
	}
	run := func(err error) string {
		if err != nil {
			return "!" + err.Error()
		}
		return "ok"
	}
	ctx := context.Background()
	failures := 0
	for index, c := range cases {
		state, err := NewReplicatedState(chordjson.ObjectOf("a", 0.0, "o", chordjson.ObjectOf("x", 0.0)))
		if err != nil {
			t.Fatal(err)
		}
		provider, err := NewRemoteServiceProvider(SingletonService(service))
		if err != nil {
			t.Fatal(err)
		}
		if err := Provide(provider, service, &stateChangeHolder{state}); err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		events := []string{}
		record := func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}
		subscription, err := provider.Subscribe(service.Id(), ServiceSingleton, func(_ context.Context, update ServiceProviderUpdate) {
			if update.Type == UpdateState {
				ops, err := json.Marshal(update.Ops)
				if err != nil {
					panic(err)
				}
				record(fmt.Sprintf("P %d %s", update.Sequence, ops))
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := subscription.Activate(); err != nil {
			t.Fatal(err)
		}
		var unsubscribers []func()
		var trace []string
		for step, op := range c.Ops {
			mu.Lock()
			events = []string{}
			mu.Unlock()
			result := "ok"
			switch op.Op {
			case "change":
				result = run(state.Edit(ctx, func(draft *overlay.Object) error {
					for _, edit := range op.Edits {
						if err := edit.apply(draft); err != nil {
							return err
						}
					}
					switch op.Nested {
					case "change":
						record("nested " + run(state.Edit(ctx, func(inner *overlay.Object) error { return inner.Set("a", 9.0) })))
					case "replace":
						record("nested " + run(state.Replace(ctx, chordjson.ObjectOf("o", chordjson.ObjectOf("x", 9.0)))))
					}
					if op.Throws {
						return errors.New("mutate failed")
					}
					return nil
				}))
			case "replace":
				result = run(state.Replace(ctx, op.Value))
			case "subscribe":
				listener := len(unsubscribers)
				react := op.React
				received := 0
				unsubscribe, err := state.Subscribe(func(current *chordjson.Object, _ context.Context, delivery ReplicatedStateDelivery) {
					received++
					encoded, err := json.Marshal(current)
					if err != nil {
						panic(err)
					}
					record(fmt.Sprintf("L%d %s %d %s", listener, delivery.Kind, delivery.Sequence, encoded))
					if react != nil && react.At == received {
						record(fmt.Sprintf("L%d reacts %s", listener, run(state.Edit(ctx, react.Edit.apply))))
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				unsubscribers = append(unsubscribers, unsubscribe)
			case "unsubscribe":
				unsubscribers[op.Index]()
			}
			value, err := json.Marshal(state.Value())
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			got := observation{Result: result, Events: make([]string, 0, len(events)), Value: string(value)}
			for _, event := range events {
				got.Events = append(got.Events, canonical(event))
			}
			mu.Unlock()
			want := c.Observed[step]
			want.Value = canonicalJSON(t, []byte(want.Value))
			for i, event := range want.Events {
				want.Events[i] = canonical(event)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			trace = append(trace, fmt.Sprintf("step %d\n      got  %s\n      want %s", step, gotJSON, wantJSON))
			if !bytes.Equal(gotJSON, wantJSON) {
				failures++
				if failures <= 3 {
					t.Errorf("scenario %d differs from Pi:\n    %s", index, strings.Join(trace[max(0, len(trace)-3):], "\n    "))
				}
				break
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, scenarios)
	}
}
