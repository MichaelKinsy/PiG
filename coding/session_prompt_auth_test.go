package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session.ts:1679-1690: when the provider has no usable auth but the published availability reports OAuth, prompt() asks for a new login instead of reporting a missing API key.
func TestValidatePromptModelAuthReportsFailedOAuth(t *testing.T) {
	h := newModelExtensionHarness(t, []bool{false}, "", false, extension.Extension{}, nil)
	if _, err := h.session.ModelRuntime().GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := &h.session.modelRuntime.availability
	state.mu.Lock()
	state.snapshot.auth = map[string]*ai.AuthCheck{"faux": {Type: ai.CredentialOAuth}}
	state.mu.Unlock()
	want := `Authentication failed for "faux". Credentials may have expired or network is unavailable. Run '/login faux' to re-authenticate.`
	if err := h.session.ValidatePromptModelAuth(t.Context()); err == nil || err.Error() != want {
		t.Fatalf("error=%v, want %q", err, want)
	}
	if _, err := h.session.Prompt(t.Context(), "hi", nil); err == nil || err.Error() != want {
		t.Fatalf("prompt error=%v, want %q", err, want)
	}
}
