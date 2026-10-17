package coding

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func customStateEntry(t *testing.T, id string, data any) icodingagent.SessionEntry {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"type": "custom", "id": id, "customType": VirtualModelStateEntry, "data": data})
	if err != nil {
		t.Fatal(err)
	}
	return sessionentry.DecodeSessionEntry(raw)
}

// packages/coding-agent/src/core/virtual-models.ts:35-39,148-156 (VirtualModelStateData provider, modelId, state;
// getVirtualModelState): the router state of a virtual model is the state of the newest `pi.virtual-model-state` custom
// entry whose provider and modelId both match; other models' entries and entries without data are skipped, and
// no entry means no state.
func TestGetVirtualModelStateReadsTheNewestMatchingEntry(t *testing.T) {
	older := extension.VirtualModelStateData{Provider: "router", ModelID: "fast", State: json.RawMessage(`{"n":1}`)}
	other := extension.VirtualModelStateData{Provider: "router", ModelID: "slow", State: json.RawMessage(`{"n":9}`)}
	newer := extension.VirtualModelStateData{Provider: "router", ModelID: "fast", State: json.RawMessage(`{"n":2}`)}
	branch := []icodingagent.SessionEntry{
		customStateEntry(t, "1", older), customStateEntry(t, "2", newer), customStateEntry(t, "3", other),
		customStateEntry(t, "4", nil),
	}
	if got := GetVirtualModelState(branch, "router", "fast"); !bytes.Equal(got, []byte(`{"n":2}`)) {
		t.Fatalf("state = %s, want the newest matching entry's {\"n\":2}", got)
	}
	if got := GetVirtualModelState(branch, "router", "slow"); !bytes.Equal(got, []byte(`{"n":9}`)) {
		t.Fatalf("slow state = %s", got)
	}
	if got := GetVirtualModelState(branch, "other-provider", "fast"); got != nil {
		t.Fatalf("a different provider's state = %s, want none", got)
	}
	if got := GetVirtualModelState(branch[:0], "router", "fast"); got != nil {
		t.Fatalf("empty branch state = %s, want none", got)
	}
	encoded, err := json.Marshal(newer)
	if err != nil || string(encoded) != `{"provider":"router","modelId":"fast","state":{"n":2}}` {
		t.Fatalf("wire shape = %s, %v", encoded, err)
	}
}
