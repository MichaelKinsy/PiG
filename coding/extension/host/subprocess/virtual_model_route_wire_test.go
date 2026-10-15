package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// virtual-models.ts:55-72 ModelRouteRequest: a router that runs in an extension receives the previous and failed physical models with their thinking levels,
// the failed response itself, and every message of the request, over the wire.
func TestVirtualModelRouteSendsPreviousFailedAndMessagesToTheExtension(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != RequestVirtualModelRoute {
			return nil
		}
		return &ResponsePayload{Result: json.RawMessage(`{"model":{"provider":"anthropic","id":"physical"},"thinkingLevel":"off"}`)}
	}
	if _, err := loadWireExt(t, host, w, &RegisterPayload{Name: "router", VirtualModels: []VirtualModelDecl{{Provider: "router", ID: "auto", Name: "Auto"}}}); err != nil {
		t.Fatal(err)
	}
	def := host.Runtime().PendingVirtualModelRegistrations()[0].Definition
	failedMessage := ai.AssistantMessage{Provider: "anthropic", Model: "large", StopReason: ai.StopReasonError, ErrorMessage: "overloaded_error", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}}}
	if _, err := def.Route(t.Context(), extension.ModelRouteRequest{
		Model: &ai.Model{ID: "auto"}, ThinkingLevel: ai.ThinkingLow, Reason: extension.ModelRouteReasonRetry,
		Previous: &extension.ModelRoutePrevious{Model: &ai.Model{ID: "small", ProviderMeta: ai.ProviderMetadata{ProviderID: "anthropic"}}, ThinkingLevel: ai.ThinkingMedium},
		Failed:   &extension.ModelRouteFailed{Model: &ai.Model{ID: "large", ProviderMeta: ai.ProviderMetadata{ProviderID: "anthropic"}}, ThinkingLevel: ai.ThinkingHigh, Message: failedMessage},
		Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("first")}, ai.UserMessage{Content: ai.UserText("second")}},
	}); err != nil {
		t.Fatal(err)
	}
	env := <-w.requests
	var args VirtualModelRouteArgs
	if err := json.Unmarshal(env.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	request := args.Request
	if request.Previous == nil || modelMember(request.Previous.Model, "id") != "small" || request.Previous.ThinkingLevel != "medium" {
		t.Fatalf("previous = %+v, want small at medium", request.Previous)
	}
	if request.Failed == nil || modelMember(request.Failed.Model, "id") != "large" || request.Failed.ThinkingLevel != "high" {
		t.Fatalf("failed = %+v, want large at high", request.Failed)
	}
	var failed struct {
		ErrorMessage string `json:"errorMessage"`
		StopReason   string `json:"stopReason"`
	}
	if err := json.Unmarshal(request.Failed.Message, &failed); err != nil || failed.ErrorMessage != "overloaded_error" || failed.StopReason != "error" {
		t.Fatalf("failed message = %s (%v), want the failed response", request.Failed.Message, err)
	}
	if len(request.Messages) != 2 || string(request.Messages[1]) == "" || string(request.Messages[0]) == string(request.Messages[1]) {
		t.Fatalf("messages = %s, want both request messages", request.Messages)
	}
}
