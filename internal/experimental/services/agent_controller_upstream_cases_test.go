package services_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableadapter"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

func controllerHost(t *testing.T, controller services.AgentController) *chord.FacetHost {
	t.Helper()
	// Go uses a typed token constructed from the production service ID; types and values cannot both be named AgentController.
	definition := chord.DefineService[services.AgentController](services.AgentControllerID)
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{{Id: "test-agent-controller", Setup: func(env *chord.FacetEnvironment) error {
		return chord.ProvideService[services.AgentController](env, definition, controller)
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return host
}

func viewOf(t *testing.T, conversation *durableadapter.Session) services.ConversationView {
	t.Helper()
	state, err := conversation.ViewState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Dispose()
	return state.Value()
}

func inboxModes(t *testing.T, view services.ConversationView) []string {
	t.Helper()
	var inbox harness.InboxState
	encoded, err := json.Marshal(view.Docs[services.InboxDocKind])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &inbox); err != nil {
		t.Fatal(err)
	}
	modes := []string{}
	for _, item := range inbox.Items {
		modes = append(modes, string(item.Mode))
	}
	return modes
}

func TestUpstreamAgentController(t *testing.T) {
	// packages/coding-agent/test/experimental-agent-controller.test.ts:11
	t.Run("prompts the root conversation through the service catalogue", func(t *testing.T) {
		durable := durabletest.OpenFauxConversation(durabletest.Text("hello back"))
		t.Cleanup(func() { _ = durable.Close(context.Background()) })
		host := controllerHost(t, services.CreateAgentController(durable.Harness, durable.Conversation))
		if got := host.Services().Catalogue(); !reflect.DeepEqual(got, []chord.ServiceCatalogueEntry{{ServiceId: "pi.agent-controller", Mode: chord.ServiceSingleton}}) {
			t.Fatalf("catalogue = %#v", got)
		}
		raw, err := host.Services().Invoke(t.Context(), chord.ServiceCall{ServiceId: services.AgentControllerID, Member: "prompt", Args: []json.RawMessage{json.RawMessage(`{"message":"hello","images":null}`)}})
		if err != nil {
			t.Fatal(err)
		}
		var response services.AgentOperationResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		if !response.Accepted || response.OperationID == nil || response.Error != nil {
			t.Fatalf("response = %s", raw)
		}
		operationID, _ := json.Marshal(*response.OperationID)
		settled, err := host.Services().Invoke(t.Context(), chord.ServiceCall{ServiceId: services.AgentControllerID, Member: "waitForPrompt", Args: []json.RawMessage{operationID}})
		if err != nil || string(settled) != `{"status":"done","text":"hello back","reason":null}` {
			t.Fatalf("waitForPrompt = %s, %v", settled, err)
		}
	})

	// packages/coding-agent/test/experimental-agent-controller.test.ts:43
	t.Run("rejects a prompt while busy and queues steering and follow-up input", func(t *testing.T) {
		pending, reached := durabletest.Pending()
		durable := durabletest.OpenFauxConversation(pending)
		t.Cleanup(func() { _ = durable.Close(context.Background()) })
		controller := services.CreateAgentController(durable.Harness, durable.Conversation)
		ctx := t.Context()

		first, err := controller.Prompt(ctx, services.AgentPromptRequest{Message: "first"})
		if err != nil || !first.Accepted {
			t.Fatalf("first prompt = %#v, %v", first, err)
		}
		<-reached

		second, err := controller.Prompt(ctx, services.AgentPromptRequest{Message: "second"})
		if err != nil || second.Accepted || second.OperationID != nil || second.Error == nil || second.Error.Code != "busy" || second.Error.Message == "" {
			t.Fatalf("second prompt = %#v, %v", second, err)
		}
		steer, err := controller.Steer(ctx, services.AgentPromptRequest{Message: "steer"})
		if err != nil || !steer.Accepted || steer.EntryID == nil || steer.Error != nil {
			t.Fatalf("steer = %#v, %v", steer, err)
		}
		followUp, err := controller.FollowUp(ctx, services.AgentPromptRequest{Message: "later"})
		if err != nil || !followUp.Accepted || followUp.EntryID == nil || followUp.Error != nil {
			t.Fatalf("followUp = %#v, %v", followUp, err)
		}
		if got := inboxModes(t, viewOf(t, durable.Conversation)); !reflect.DeepEqual(got, []string{"steer", "followUp"}) {
			t.Fatalf("inbox modes = %v", got)
		}

		for _, step := range []struct{ id, outcome string }{{*followUp.EntryID, "cancelled"}, {*followUp.EntryID, "already_consumed"}, {"999", "not_found"}, {"not-an-id", "not_found"}} {
			got, err := controller.CancelQueued(ctx, step.id)
			if err != nil || got.Outcome != step.outcome {
				t.Fatalf("cancelQueued(%s) = %#v, %v; want %s", step.id, got, err, step.outcome)
			}
		}

		if err := controller.Abort(ctx); err != nil {
			t.Fatal(err)
		}
		var live harness.LiveState
		encoded, err := json.Marshal(viewOf(t, durable.Conversation).Docs[services.LiveDocKind])
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &live); err != nil || live.Run != nil {
			t.Fatalf("live run after abort = %+v, %v", live.Run, err)
		}
		unanswered, err := controller.WaitForPrompt(ctx, *first.OperationID)
		if err != nil || unanswered.Status != "unanswered" || unanswered.Text != nil || unanswered.Reason == nil {
			t.Fatalf("waitForPrompt = %#v, %v", unanswered, err)
		}
	})

	// packages/coding-agent/test/experimental-agent-controller.test.ts:100
	t.Run("starts a compaction task", func(t *testing.T) {
		durable := durabletest.OpenFauxConversation()
		t.Cleanup(func() { _ = durable.Close(context.Background()) })
		controller := services.CreateAgentController(durable.Harness, durable.Conversation)
		got, err := controller.Compact(t.Context(), services.AgentCompactionRequest{CustomInstructions: new("short")})
		if err != nil || !got.Accepted || got.OperationID == nil || got.Error != nil {
			t.Fatalf("compact = %#v, %v", got, err)
		}
	})
}
