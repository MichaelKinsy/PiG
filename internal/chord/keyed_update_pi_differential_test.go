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

// keyedCaptureTransport serves the oracle's initial keyed snapshot (k1:1 with states s and t and method m, k2:1 with state s and method m)
// and keeps the binding's update listener so the test can deliver provider updates directly, as the oracle's transport does.
type keyedCaptureTransport struct {
	mu       sync.Mutex
	listener UpdateListener
}

func (*keyedCaptureTransport) Invoke(context.Context, ServiceCall) (json.RawMessage, error) {
	return nil, nil
}

func (t *keyedCaptureTransport) Subscribe(_ context.Context, serviceId string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	t.mu.Lock()
	t.listener = listener
	t.mu.Unlock()
	var instances []ServiceInstanceSnapshot
	initial := `[{"instance":{"key":"k1","generation":1},"members":[{"name":"s","kind":"state","sequence":0,"ops":[["r",{"a":1}]]},{"name":"t","kind":"state","sequence":0,"ops":[["r",{"a":1}]]},{"name":"m","kind":"method"}]},` +
		`{"instance":{"key":"k2","generation":1},"members":[{"name":"s","kind":"state","sequence":0,"ops":[["r",{"a":2}]]},{"name":"m","kind":"method"}]}]`
	if err := json.Unmarshal([]byte(initial), &instances); err != nil {
		return nil, err
	}
	return updateCaptureSubscription{ServiceSubscriptionSnapshot{ServiceId: serviceId, Mode: mode, Instances: instances}}, nil
}

type keyedUpdateObservation struct {
	Errors []string `json:"errors"`
	Opened []string `json:"opened"`
	Values []string `json:"values"`
}

type keyedObservedView struct {
	name    string
	service *RemoteService
}

// Pi packages/chord/src/services/consumer.ts:279-311 (KeyedBinding.observe and its stale view), 389-440 (KeyedBinding.#update and #spawn),
// 664-700 (validateResetSnapshot, validateMembers), services/instances.ts (InstanceDirectory replace, remove, observe) and
// services/state.ts:344-420 (ReplicatedStateReplica) against the installed chord 1.1.0 dist, by testdata/keyed_update_differential.mjs:
// seeded random sequences of keyed state, reset, spawned and closed updates plus singleton lifecycle updates reach an observed keyed
// service. They mix live, stale and unknown addresses, repeated live generations, repeated reset keys, missing addresses, wrong service or
// mode, kind changes, invalid members, in-order, repeated and skipped sequences, non-base ops and ops that fail to apply. After each update
// the errors reported to onError, the observations opened, and the values of s and t read through every observation opened so far (a
// closed one reads as Pi's stale-observation error) must equal Pi's, with JSON object keys compared in their order. The service ID is kud.a
// because test service views are package-global.
func TestKeyedBindingUpdatesMatchPiOnRandomSequences(t *testing.T) {
	const scenarios, steps, seed, serviceID = 300, 16, 20260704, "kud.a"
	cmd := exec.CommandContext(t.Context(), "node", "testdata/keyed_update_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(scenarios), strconv.Itoa(steps), strconv.Itoa(seed), serviceID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var cases []struct {
		Updates  []json.RawMessage        `json:"updates"`
		Observed []keyedUpdateObservation `json:"observed"`
	}
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != scenarios {
		t.Fatalf("Pi oracle returned %d scenarios, want %d", len(cases), scenarios)
	}
	canonical := func(text string) string {
		if text == "undefined" || strings.HasPrefix(text, "!") {
			return text
		}
		return canonicalJSON(t, []byte(text))
	}
	read := func(service *RemoteService, name string) string {
		replica, err := service.State(name)
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
	failures := 0
	for index, c := range cases {
		var mu sync.Mutex
		errs, opened := []string{}, []string{}
		var views []keyedObservedView
		transport := &keyedCaptureTransport{}
		binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: ServiceIDs(serviceID), Transport: transport, OnError: func(err error) {
			mu.Lock()
			errs = append(errs, err.Error())
			mu.Unlock()
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := binding.Observe(serviceID, func(_ context.Context, service *RemoteService) error {
			address := service.Address()
			name := fmt.Sprintf("%s:%d", address.Key, address.Generation)
			mu.Lock()
			opened = append(opened, name)
			views = append(views, keyedObservedView{name, service})
			mu.Unlock()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := binding.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		observe := func() keyedUpdateObservation {
			mu.Lock()
			got := keyedUpdateObservation{Errors: errs, Opened: opened, Values: []string{}}
			errs, opened = []string{}, []string{}
			current := append([]keyedObservedView(nil), views...)
			mu.Unlock()
			for _, view := range current {
				got.Values = append(got.Values, fmt.Sprintf("%s s=%s t=%s", view.name, read(view.service, "s"), read(view.service, "t")))
			}
			return got
		}
		var trace []string
		mismatch := false
		compare := func(label string, want keyedUpdateObservation) {
			for i, value := range want.Values {
				name, rest, _ := strings.Cut(value, " s=")
				s, tValue, _ := strings.Cut(rest, " t=")
				want.Values[i] = fmt.Sprintf("%s s=%s t=%s", name, canonical(s), canonical(tValue))
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
