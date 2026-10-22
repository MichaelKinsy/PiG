package extensionconformance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func registryEdgesHarnessCases() []harnessCase {
	return append(sdkHarnessCases(), packedSDKHarnessCases()...)
}

// registryProbeModelNamed returns the probe state's model with this id.
func registryProbeModelNamed(t *testing.T, id string) map[string]any {
	t.Helper()
	for _, model := range registryProbeState()["models"].([]any) {
		if model.(map[string]any)["id"] == id {
			return model.(map[string]any)
		}
	}
	t.Fatalf("no probe model %q", id)
	return nil
}

func notifiedText(t *testing.T, h *harness) string {
	t.Helper()
	notes := h.ui.Recorded()
	if len(notes) != 1 {
		t.Fatalf("notifications: %v", notes)
	}
	// recordingUI appends the notification level.
	return strings.TrimSuffix(notes[0], ":info")
}

func runRegistryCommand(t *testing.T, h *harness, name string) string {
	t.Helper()
	command, ok := findCommand(h.runner, name)
	if !ok {
		t.Fatalf("missing %s command", name)
	}
	h.ui.ClearRecorded()
	if err := command.Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	return notifiedText(t, h)
}

func closeRegistryHarness(t *testing.T, h *harness) {
	t.Helper()
	t.Cleanup(func() {
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	})
}

// Every SDK answers the registry reads that depend on the provider's state the way Pi's ModelRegistry does
// (model-registry.ts:68-87, 206-208; model-runtime.ts:543-553):
//   - getAvailable is the models of the providers with configured auth, narrowed to a provider's availableModelIds;
//   - hasConfiguredAuth and isUsingOAuth read the model's provider, and a model with no provider has neither
//     (the Set and Map lookups of an undefined provider miss);
//   - find is a lookup by exact provider and model id that ignores whether the provider has auth, and a pair that no model
//     has is absent, not an error.
//
// The probe state (registryProbeState) gives every one of those a different answer from an SDK's fallback: getAll,
// a configured flag that defaults to true, an ignored availableModelIds list, or an error on a missing provider.
func TestRegistryProviderStateReadsAcrossSDKs(t *testing.T) {
	probe := func(id string) any { return registryProbeModelNamed(t, id) }
	// The models in registryProbeState order: a, b, c, d, e, and then a model with no provider.
	want := map[string]any{
		"available": []any{probe("a"), probe("c")},
		"hasAuth":   []any{true, true, true, false, true, false},
		"oauth":     []any{true, true, false, false, true, false},
		"found":     probe("a"),
		"foundOff":  probe("d"),
		"missing":   nil,
		"mismatch":  nil,
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var wantDecoded any
	if err := json.Unmarshal(wantJSON, &wantDecoded); err != nil {
		t.Fatal(err)
	}
	for _, tc := range registryEdgesHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			closeRegistryHarness(t, h)
			state := registryProbeState()
			h.bridge.SetHostAction("getModelRegistryState", func() map[string]any { return state })
			h.bridge.SetHostAction("getModel", func(provider, modelID string) json.RawMessage {
				for _, model := range registryProbeState()["models"].([]any) {
					if model.(map[string]any)["provider"] == provider && model.(map[string]any)["id"] == modelID {
						data, _ := json.Marshal(model)
						return data
					}
				}
				return nil
			})
			h.host.BroadcastStateUpdate()
			text := runRegistryCommand(t, h, "registry-edges")
			var got any
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatalf("decode %q: %v", text, err)
			}
			if !reflect.DeepEqual(got, wantDecoded) {
				t.Fatalf("got %s\nwant %s", text, wantJSON)
			}
		})
	}
}

// Pi's find is a synchronous lookup that answers undefined only when no model matches. PiG's native SDKs ask the host, and a
// host that cannot answer is not "no such model": every SDK reports the failure (the rule of
// extensions/sdk/getter_state_test.go: a failed host call is never an empty value). The same holds for a session read:
// a host with no session to read answers an error, so a list-typed read is never a null where an empty list would be believed.
// Node is not in this row: its find reads the registry snapshot the host pushed and makes no host call.
func TestHostFailureIsNotAbsentInNativeSDKs(t *testing.T) {
	const (
		findFailure    = "model lookup is not bound"
		sessionFailure = "session reads are not bound"
	)
	var cases []harnessCase
	for _, tc := range registryEdgesHarnessCases() {
		if !strings.Contains(tc.name, "node") {
			cases = append(cases, tc)
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			closeRegistryHarness(t, h)
			// A host whose actions are all unbound: the real dispatcher answers an error for getModel and sessionRead.
			h.bridge.SetActions(&subprocess.HostCallbacks{})
			text := runRegistryCommand(t, h, "registry-unbound-reads")
			var got map[string]string
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatalf("decode %q: %v", text, err)
			}
			for key, message := range map[string]string{"find": findFailure, "entries": sessionFailure} {
				value, ok := got[key]
				if !ok || !strings.HasPrefix(value, "error:") || !strings.Contains(value, message) {
					t.Errorf("%s = %q, want a host error naming %q (report: %s)", key, value, message, text)
				}
			}
		})
	}
}
