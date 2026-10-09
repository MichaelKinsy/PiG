package experimental

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// clientTuiScriptedController answers the three admission calls with canned responses once released, so a test sees the interim status the component sets before the response and the status the response replaces it with.
type clientTuiScriptedController struct {
	services.AgentController
	release   chan struct{}
	operation services.AgentOperationResponse
	queue     services.AgentQueueResponse
}

func (controller *clientTuiScriptedController) wait(ctx context.Context) error {
	select {
	case <-controller.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (controller *clientTuiScriptedController) Prompt(ctx context.Context, _ services.AgentPromptRequest) (services.AgentOperationResponse, error) {
	return controller.operation, controller.wait(ctx)
}

func (controller *clientTuiScriptedController) Steer(ctx context.Context, _ services.AgentPromptRequest) (services.AgentQueueResponse, error) {
	return controller.queue, controller.wait(ctx)
}

func (controller *clientTuiScriptedController) FollowUp(ctx context.Context, _ services.AgentPromptRequest) (services.AgentQueueResponse, error) {
	return controller.queue, controller.wait(ctx)
}

// upstream: packages/coding-agent/src/experimental/client-tui.ts:522-596. An unknown slash command, a submitted prompt, a steering message while a run is live and a follow-up each set their interim status before the service answers, and the answer's report replaces it: an empty status for an accepted operation, "Operation rejected: <message>", "Queued <entry>." or "Message rejected: <message>".
func TestClientTuiStatusTexts(t *testing.T) {
	rejected := &services.AgentOperationError{Code: "busy", Message: "a run is active"}
	for _, row := range []struct {
		name    string
		live    bool
		start   func(*ExperimentalClientTui)
		script  clientTuiScriptedController
		interim string
		final   string
	}{
		{name: "an unknown slash command", start: func(c *ExperimentalClientTui) { c.runPrompt("/nope args") }, final: "Unknown slash command: /nope"},
		{name: "an accepted prompt", start: func(c *ExperimentalClientTui) { c.runPrompt("hello") }, script: clientTuiScriptedController{operation: services.AgentOperationResponse{Accepted: true, OperationID: new("op-1")}}, interim: "Running turn…", final: ""},
		{name: "a rejected prompt", start: func(c *ExperimentalClientTui) { c.runPrompt("hello") }, script: clientTuiScriptedController{operation: services.AgentOperationResponse{Error: rejected}}, interim: "Running turn…", final: "Operation rejected: a run is active"},
		{name: "a queued steering message", live: true, start: func(c *ExperimentalClientTui) { c.runPrompt("hello") }, script: clientTuiScriptedController{queue: services.AgentQueueResponse{Accepted: true, EntryID: new("12")}}, interim: "Queueing steering message…", final: "Queued 12."},
		{name: "a rejected steering message", live: true, start: func(c *ExperimentalClientTui) { c.runPrompt("hello") }, script: clientTuiScriptedController{queue: services.AgentQueueResponse{Error: rejected}}, interim: "Queueing steering message…", final: "Message rejected: a run is active"},
		{name: "a queued follow-up", start: func(c *ExperimentalClientTui) { c.queueFollowUp("later") }, script: clientTuiScriptedController{queue: services.AgentQueueResponse{Accepted: true, EntryID: new("13")}}, interim: "Queueing follow-up…", final: "Queued 13."},
		{name: "a rejected follow-up", start: func(c *ExperimentalClientTui) { c.queueFollowUp("later") }, script: clientTuiScriptedController{queue: services.AgentQueueResponse{Error: rejected}}, interim: "Queueing follow-up…", final: "Message rejected: a run is active"},
	} {
		t.Run(row.name, func(t *testing.T) {
			observation := newClientTuiObservation(t)
			controller := row.script
			controller.release = make(chan struct{})
			lifetime, cancel := context.WithCancel(t.Context())
			component := &ExperimentalClientTui{ctx: lifetime, cancel: cancel, cwd: t.TempDir(), controller: &controller, requestRender: observation.RequestRender, runOnMain: observation.Executor.RunOnMain, queueMicrotask: observation.Executor.QueueMicrotask}
			t.Cleanup(func() {
				if err := component.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := observation.Executor.RunOnMain(t.Context(), func() {
				component.initialize()
				if row.live {
					view := conversationView(nil, &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}}, nil)
					component.conversation = &view
				}
				row.start(component)
				if row.interim != "" && component.status != row.interim {
					t.Errorf("interim status = %q, want %q", component.status, row.interim)
				}
			}); err != nil {
				t.Fatal(err)
			}
			close(controller.release)
			// A mutated status never settles; the deadline turns that into a failure instead of a hang.
			waitContext, stop := context.WithTimeout(t.Context(), 20*time.Second)
			defer stop()
			for {
				var status, rendered string
				if err := observation.Executor.RunOnMain(waitContext, func() {
					status = component.status
					rendered = strings.Join(component.Render(120), "\n")
				}); err != nil {
					t.Fatal(err)
				}
				if status == row.final && strings.Contains(rendered, row.final) {
					break
				}
				select {
				case <-waitContext.Done():
					t.Fatalf("status = %q, want %q\n%s", status, row.final, rendered)
				case <-observation.changed:
				}
			}
		})
	}
}
