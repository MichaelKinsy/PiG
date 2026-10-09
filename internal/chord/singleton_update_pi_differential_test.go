package chord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type updateCaptureSubscription struct{ snapshot ServiceSubscriptionSnapshot }

func (s updateCaptureSubscription) Snapshot() ServiceSubscriptionSnapshot { return s.snapshot }
func (updateCaptureSubscription) Activate() error                         { return nil }
func (updateCaptureSubscription) Close(context.Context) error             { return nil }

// updateCaptureTransport serves the oracle's initial singleton snapshot and keeps the binding's update listener so the test can deliver
// provider updates directly, as the oracle's transport does.
type updateCaptureTransport struct {
	mu       sync.Mutex
	listener UpdateListener
}

func (*updateCaptureTransport) Invoke(context.Context, ServiceCall) (json.RawMessage, error) {
	return nil, nil
}

func (t *updateCaptureTransport) Subscribe(_ context.Context, serviceId string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	t.mu.Lock()
	t.listener = listener
	t.mu.Unlock()
	var instance ServiceInstanceSnapshot
	members := `{"members":[{"name":"s","kind":"state","sequence":0,"ops":[["r",{"a":0}]]},{"name":"t","kind":"state","sequence":0,"ops":[["r",{"a":0}]]},{"name":"m","kind":"method"}]}`
	if err := json.Unmarshal([]byte(members), &instance); err != nil {
		return nil, err
	}
	return updateCaptureSubscription{ServiceSubscriptionSnapshot{ServiceId: serviceId, Mode: mode, Instances: []ServiceInstanceSnapshot{instance}}}, nil
}

type singletonUpdateObservation struct {
	Errors     []string `json:"errors"`
	Deliveries []string `json:"deliveries"`
	S          string   `json:"s"`
	T          string   `json:"t"`
}

// Pi packages/chord/src/services/consumer.ts:596-621 (the singleton subscription listener), 173-205 (ServiceFacade.install and update),
// 664-700 (validateResetSnapshot, validateMembers) and services/state.ts:344-420 (ReplicatedStateReplica hydrate, update, clear) against
// the installed chord 1.1.0 dist, by testdata/singleton_update_differential.mjs: seeded random sequences of state, reset, unavailable,
// replaced, spawned and closed updates (in-order, repeated and skipped sequences; kind changes; duplicate or empty member names; wrong
// service, mode or address; non-base reset ops; ops that fail to apply) reach a ready singleton binding with a subscriber on member s. After
// each update, the errors reported to onError, the subscriber's deliveries and the values of s and t must equal Pi's, with JSON object keys compared in their order. The service ID is
// sud.a because test service views are package-global.
func TestSingletonBindingUpdatesMatchPiOnRandomSequences(t *testing.T) {
	const scenarios, steps, seed, serviceID = 300, 16, 20260703, "sud.a"
	cmd := exec.CommandContext(t.Context(), "node", "testdata/singleton_update_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(scenarios), strconv.Itoa(steps), strconv.Itoa(seed), serviceID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var cases []struct {
		Updates  []json.RawMessage            `json:"updates"`
		Observed []singletonUpdateObservation `json:"observed"`
	}
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != scenarios {
		t.Fatalf("Pi oracle returned %d scenarios, want %d", len(cases), scenarios)
	}
	failures := 0
	for index, c := range cases {
		var mu sync.Mutex
		errs, deliveries := []string{}, []string{}
		transport := &updateCaptureTransport{}
		binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: ServiceIDs(serviceID), Transport: transport, OnError: func(err error) {
			mu.Lock()
			errs = append(errs, err.Error())
			mu.Unlock()
		}})
		if err != nil {
			t.Fatal(err)
		}
		handle, err := binding.Use(serviceID)
		if err != nil {
			t.Fatal(err)
		}
		s, err := handle.State("s")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Subscribe(func(value JsonValue, _ context.Context, delivery ReplicatedStateDelivery) error {
			encoded, _ := json.Marshal(value)
			mu.Lock()
			deliveries = append(deliveries, fmt.Sprintf("%s:%d:%s", delivery.Kind, delivery.Sequence, encoded))
			mu.Unlock()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := binding.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		read := func(name string) string {
			replica, err := handle.State(name)
			if err != nil {
				return "!" + err.Error()
			}
			value, hydrated, err := replica.Load()
			switch {
			case err != nil:
				return "!" + err.Error()
			case !hydrated:
				return "undefined"
			}
			encoded, _ := json.Marshal(value)
			return string(encoded)
		}
		observe := func() singletonUpdateObservation {
			mu.Lock()
			defer mu.Unlock()
			got := singletonUpdateObservation{Errors: errs, Deliveries: deliveries}
			errs, deliveries = []string{}, []string{}
			got.S, got.T = read("s"), read("t")
			return got
		}
		var trace []string
		mismatch := false
		compare := func(label string, want singletonUpdateObservation) {
			canonical := func(text string) string {
				if text == "undefined" || strings.HasPrefix(text, "!") {
					return text
				}
				return canonicalJSON(t, []byte(text))
			}
			want.S, want.T = canonical(want.S), canonical(want.T)
			for i, delivery := range want.Deliveries {
				parts := strings.SplitN(delivery, ":", 3)
				want.Deliveries[i] = parts[0] + ":" + parts[1] + ":" + canonical(parts[2])
			}
			got := observe()
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			trace = append(trace, fmt.Sprintf("%s\n      got  %s\n      want %s", label, gotJSON, wantJSON))
			if string(gotJSON) != string(wantJSON) {
				mismatch = true
			}
		}
		compare("ready", c.Observed[0])
		transport.mu.Lock()
		listener := transport.listener
		transport.mu.Unlock()
		for step, raw := range c.Updates {
			if mismatch {
				break
			}
			var update ServiceProviderUpdate
			if err := json.Unmarshal(raw, &update); err != nil {
				t.Fatalf("scenario %d step %d: decode %s: %v", index, step, raw, err)
			}
			listener(context.Background(), update)
			compare(string(raw), c.Observed[step+1])
		}
		if err := binding.Dispose(t.Context()); err != nil {
			t.Fatal(err)
		}
		if mismatch {
			failures++
			if failures <= 3 {
				t.Errorf("scenario %d differs from Pi:\n    %s", index, strings.Join(trace, "\n    "))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, scenarios)
	}
}
