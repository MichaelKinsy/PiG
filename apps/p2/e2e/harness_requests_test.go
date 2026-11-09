// Ports test/p2/harness-requests.test.ts (Pi c9552c7d): the Harness's Models remembers each generation request, and an
// `afterResponse` hook finds the request that produced its message (`HarnessParts.RequestOf`), once. Pi keys the request by
// the message object `result()` resolved with and pins that Durable hands the hook that very object. Durable's Go hook gets
// a copy, so p2 keys it by the conversation's provider session ID and checks the response (runtime/model_requests.go); this
// test pins that the hook finds the request of the response it got.

package e2e_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

func TestAfterResponseHooksFindTheRequestThatProducedTheirMessageOnce(t *testing.T) {
	var (
		mu    sync.Mutex
		w     *workerHost
		found []struct {
			message        ai.AssistantMessage
			request, again *p2runtime.ModelRequest
		}
	)
	probe := &durable.Extension{
		Name: "probe",
		Hooks: []durable.HookRegistration{harness.Hook(harness.GenerationTask, &harness.GenerationHooks{
			AfterResponse: func(ctx context.Context, message ai.AssistantMessage, api harness.HookApi) error {
				request, err := w.parts.RequestOf(ctx, api, message)
				if err != nil {
					return err
				}
				again, err := w.parts.RequestOf(ctx, api, message)
				if err != nil {
					return err
				}
				mu.Lock()
				defer mu.Unlock()
				found = append(found, struct {
					message        ai.AssistantMessage
					request, again *p2runtime.ModelRequest
				}{message, request, again})
				return nil
			},
		})},
	}
	extension := chordhost.DefineFacet(func(_ chordhost.FacetHost, self chordhost.FacetSelf) error {
		return p2runtime.Extensions.Contribute(self, p2runtime.Entry("probe", &p2runtime.P2Extension{Extension: probe}))
	})
	w = newWorkerHost(t, p2runtime.Location{Cwd: checkout(t)}, nil, chordhost.HostFacet{Name: "probe", Facet: extension})
	session, err := w.parts.Durable()
	if err != nil {
		t.Fatal(err)
	}
	session.Resume()
	root, err := session.Root(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// One user input: the fake model answers it in one request.
	submission, err := root.Submit(context.Background(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := submission.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.WaitForIdle(context.Background()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(found) != 1 {
		t.Fatalf("afterResponse ran %d times, want 1", len(found))
	}
	got := found[0]
	if got.message.StopReason != ai.StopReasonStop {
		t.Fatalf("stopReason = %q, want stop", got.message.StopReason)
	}
	if got.request == nil {
		t.Fatal("RequestOf finds the message afterResponse got")
	}
	if got.again != nil {
		t.Fatal("each response is taken once")
	}
	if got.request.Model.Provider.ID() != "faux" || got.request.Model.ID != "faux-1" {
		t.Fatalf("request model = %s/%s, want faux/faux-1", got.request.Model.Provider.ID(), got.request.Model.ID)
	}
	var roles []string
	for _, sent := range got.request.Messages {
		roles = append(roles, sent.MessageRole())
	}
	// The worker's plugins add system messages (the prompt) before the input; Pi's test loads none.
	if len(roles) == 0 || roles[len(roles)-1] != "user" || slices.ContainsFunc(roles[:len(roles)-1], func(role string) bool { return role != "system" }) {
		t.Fatalf("request roles = %v, want system messages then the user input", roles)
	}
	// Durable's options and the Harness's identity, without the attempt's own.
	options := got.request.Options
	if options.SessionID == "" || options.Identity == nil {
		t.Fatalf("options: sessionId %q, identity %v; want both set", options.SessionID, options.Identity)
	}
	if options.Signal != nil || options.Telemetry != nil || options.OnPayload != nil {
		t.Fatal("the attempt's signal, telemetry and onPayload are not part of the request")
	}
}
