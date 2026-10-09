package subprocess

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream _emitModelSelect (agent-session.ts:2372-2384) passes the Session's Model objects, so a subprocess extension reads event.model.provider and event.model.id, and previousModel is undefined when the Session had no model.
func TestModelSelectEventCarriesPiModelShape(t *testing.T) {
	next := &ai.Model{ID: "probe-model-2", DisplayName: "Probe 2", ProviderMeta: ai.ProviderMetadata{ProviderID: "probe"}}
	previous := &ai.Model{ID: "probe-model", ProviderMeta: ai.ProviderMetadata{ProviderID: "probe"}}
	for _, tc := range []struct {
		name         string
		previous     *ai.Model
		wantPrevious string
	}{
		{name: "changed", previous: previous, wantPrevious: "probe/probe-model"},
		{name: "first", previous: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(wireEventPayload(extension.ModelSelectEvent{Type: "model_select", Model: next, PreviousModel: tc.previous, Source: extension.ModelSelectSourceUser}))
			if err != nil {
				t.Fatal(err)
			}
			// upstream: agent-session.ts:2463-2468 builds { type, model, previousModel, source } in that order, and a JavaScript handler sees that key order.
			if !bytes.HasPrefix(data, []byte(`{"type":"model_select","model":{`)) || !bytes.HasSuffix(data, []byte(`,"source":"set"}`)) {
				t.Fatalf("model_select key order in %s", data)
			}
			var got struct {
				Model         map[string]any  `json:"model"`
				PreviousModel *map[string]any `json:"previousModel"`
				Source        string          `json:"source"`
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Model["provider"] != "probe" || got.Model["id"] != "probe-model-2" || got.Model["name"] != "Probe 2" || got.Source != "set" {
				t.Fatalf("model_select payload %s", data)
			}
			if tc.wantPrevious == "" {
				var raw map[string]json.RawMessage
				_ = json.Unmarshal(data, &raw)
				if _, present := raw["previousModel"]; present {
					t.Fatalf("previousModel present without a previous model: %s", data)
				}
				return
			}
			if got.PreviousModel == nil || (*got.PreviousModel)["provider"].(string)+"/"+(*got.PreviousModel)["id"].(string) != tc.wantPrevious {
				t.Fatalf("previousModel in %s, want %s", data, tc.wantPrevious)
			}
		})
	}
}
