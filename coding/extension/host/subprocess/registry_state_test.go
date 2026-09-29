package subprocess

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Pi ModelRuntime.registerProvider merges defined top-level values and retains
// Map insertion order; unregister then register inserts at the end (:744-797).
func TestRegistryRegistrationMergeAndOrder(t *testing.T) {
	b := NewUIBridge(nil)
	b.RecordProviderRegistration("first", json.RawMessage(`{"name":"Original","headers":{"X":"one"},"apiKey":"key"}`))
	b.RecordProviderRegistration("second", json.RawMessage(`{"apiKey":"second"}`))
	b.RecordProviderRegistration("first", json.RawMessage(`{"name":"","headers":null}`))
	state := b.ModelRegistryState()
	data, err := json.Marshal(state["registered"])
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal([]byte(`[{"name":"first","config":{"name":"","headers":null,"apiKey":"key"}},{"name":"second","config":{"apiKey":"second"}}]`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered = %s", data)
	}
	b.ForgetProviderRegistration("first")
	b.RecordProviderRegistration("first", json.RawMessage(`{}`))
	if !reflect.DeepEqual(b.registeredProviderOrder, []string{"second", "first"}) {
		t.Fatalf("reinsert order: %v", b.registeredProviderOrder)
	}
}
