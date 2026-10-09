package ai

import (
	"strings"
	"testing"
)

// upstream: packages/ai/src/compat.ts registerFauxProvider creates the faux core, registers `{ api, stream, streamSimple }` in the API registry under a
// `faux-provider-<id>` source id and returns the registration whose unregister removes that source from the registry.
func TestRegisterFauxProviderRegistersItsAPIUntilUnregistered(t *testing.T) {
	resetCompatRegistry(t)
	registration := RegisterFauxProvider(RegisterFauxProviderOptions{API: "faux-registered-api"})
	registration.SetResponses([]FauxResponseStep{FauxResponse{Content: []FauxContentBlock{FauxText("registered")}}})

	if GetAPIProvider("faux-registered-api") == nil {
		t.Fatal("registerFauxProvider registers its API")
	}
	if !strings.HasPrefix(registration.sourceID, "faux-provider-") || len(registration.sourceID) > len("faux-provider-")+8 {
		t.Fatalf("source id = %q, want faux-provider-<up to 8 characters>", registration.sourceID)
	}
	message, err := CompleteSimple(t.Context(), registration.GetModel(), compatHello, StreamOptions{})
	if err != nil || message.StopReason != StopReasonStop || message.Content[0].(TextContent).Text != "registered" {
		t.Fatalf("message = %+v, err = %v", message, err)
	}
	if registration.State().CallCount() != 1 {
		t.Fatalf("call count = %d, want the registry stream to reach the faux provider", registration.State().CallCount())
	}

	registration.Unregister()
	if GetAPIProvider("faux-registered-api") != nil {
		t.Fatal("unregister removes the API from the registry")
	}
	if _, err := Stream(t.Context(), registration.GetModel(), compatHello); err == nil || err.Error() != "No API provider registered for api: faux-registered-api" {
		t.Fatalf("stream after unregister: %v", err)
	}

	// Without options the API is generated, as `registerFauxProvider(options = {})`.
	generated := RegisterFauxProvider()
	t.Cleanup(generated.Unregister)
	if GetAPIProvider(generated.API()) == nil || !strings.HasPrefix(string(generated.API()), "faux:") {
		t.Fatalf("generated api = %q", generated.API())
	}
}
