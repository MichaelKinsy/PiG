package cli

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi rpc-types.ts RpcSessionState.model is a Model<any> whose optional `type` ("chat", ai/src/types.ts:1119) is serialized
// as the model carries it: a model that sets type reports it, a model that does not omits it.
func TestRPCModelProjectionCarriesModelType(t *testing.T) {
	typed := &ai.Model{ID: "typed", Type: ai.ModelTypeChat}
	untyped := &ai.Model{ID: "untyped"}
	list := rpcModelList([]*ai.Model{typed, untyped})
	for _, tc := range []struct {
		name  string
		model *RPCModel
		want  string
		has   bool
	}{{"state typed", rpcModelValue(typed), "chat", true}, {"state untyped", rpcModelValue(untyped), "", false}, {"available typed", list[0], "chat", true}, {"available untyped", list[1], "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.model)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			got, has := wire["type"]
			if has != tc.has || (has && got != tc.want) {
				t.Fatalf("type = %v (present %v), want %q (present %v) in %s", got, has, tc.want, tc.has, data)
			}
		})
	}
}

// Pi 1.1.0 catalog entries carry "type":"chat" (packages/ai/src/providers/data/*.json), and Pi's RPC model objects keep
// it. The member's position follows each catalog entry (gpt-4o has it last, other entries first), so only its value is
// asserted here.
func TestRPCBuiltinChatModelCarriesType(t *testing.T) {
	model := ai.GetBuiltinModel("openai", "gpt-4o")
	if model == nil {
		t.Fatal("openai/gpt-4o missing from the catalog")
	}
	data, err := json.Marshal(rpcModelValue(model))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["type"] != "chat" {
		t.Fatalf("RPC model JSON = %.160s..., want \"type\":\"chat\" as Pi's has", data)
	}
}
