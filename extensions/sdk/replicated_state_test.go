package sdk

import "testing"

// A state snapshot replicates Pi's ctx.model.id, ctx.model.provider and ctx.sessionManager.getSessionName(): the id (the model's name only when it has no id),
// an explicit null clears the model, an absent member keeps it, and every session snapshot carries the name with "" for no name. The Python and Rust SDKs
// read the same members (TestReplicatedAccessorsReadYourWritesAcrossSDKs).
func TestStateUpdateReplicatesModelIdentityAndSessionName(t *testing.T) {
	ext := New("test")
	update := func(state string) {
		t.Helper()
		ext.handleNotify(envelope{Notify: &notifyMsg{Method: "state_update", Args: []byte(`{"state":` + state + `}`)}})
	}
	ctx := Context{ext: ext}
	check := func(step, model, provider, name string) {
		t.Helper()
		if got := ctx.Model(); got != model {
			t.Errorf("%s: Model() = %q, want %q", step, got, model)
		}
		if got := ctx.ModelProvider(); got != provider {
			t.Errorf("%s: ModelProvider() = %q, want %q", step, got, provider)
		}
		if got := ctx.SessionName(); got != name {
			t.Errorf("%s: SessionName() = %q, want %q", step, got, name)
		}
	}
	check("initial", "", "", "")
	update(`{"model":{"id":"id-sentinel","name":"Name Sentinel","provider":{"id":"prov-sentinel"}},"session":{"sessionName":"s-sentinel"}}`)
	check("snapshot", "id-sentinel", "prov-sentinel", "s-sentinel")
	update(`{"isIdle":true}`)
	check("absent members keep the replica", "id-sentinel", "prov-sentinel", "s-sentinel")
	update(`{"model":{"id":"","name":"Only Name","provider":"flat-provider"},"session":{"sessionName":""}}`)
	check("name only and a flat provider", "Only Name", "flat-provider", "")
	update(`{"model":null}`)
	check("null clears the model", "", "", "")
}
