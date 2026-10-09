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
