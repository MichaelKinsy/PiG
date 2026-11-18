package coding

import (
	"testing"
)

// Direct row: the unexported buildModel, called with an explicit key for the
// store-only github-copilot/gemini-4-carbon, must return the key-carrying provider
// that base builds. It must not return the lazy catalogModelBackend binding the
// store-only shortcut yields for an unregistered provider.
//
// No request is sent: github-copilot ignores the fixture baseUrl and would reach
// the real api.individual.githubcopilot.com, so the row asserts the binding only.
func TestBuildModelKeyRowStoreOnlyCarriesExplicitKey(t *testing.T) {
	server := newNoAuthServer(t)
	svcs := noAuthRuntime(t, noAuthFixture(t, server.URL))

	model, err := buildModel(noAuthProvider+"/gemini-4-carbon", svcs, "fixture-key")
	if err != nil {
		t.Fatalf("buildModel: %v", err)
	}
	if model == nil || model.Provider == nil {
		t.Fatalf("buildModel returned model=%v with no provider", model)
	}
	if _, lazy := model.Provider.(*catalogModelBackend); lazy {
		t.Fatalf("buildModel returned the lazy catalogModelBackend binding; want the key-carrying provider as base does")
	}
	if n := len(server.received()); n != 0 {
		t.Errorf("direct row: server received %d request(s) during build, want 0", n)
	}
}
