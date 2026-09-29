package services

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

func TestUpstreamAgentController(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/experimental-agent-controller.test.ts:44
	t.Run("provides the presentation-safe AgentLane command surface", func(t *testing.T) {
		ctx := t.Context()
		var calls []string
		lane := &controllerLane{
			operation: func(got context.Context, text string, images []ai.ImageContent) (LaneOperation, error) {
				if got != ctx || text != "hello" || images != nil {
					t.Error("prompt arguments changed")
				}
				calls = append(calls, "prompt")
				return LaneOperation{OperationID: "operation-1", Status: "completed"}, nil
			},
			abort: func(got context.Context, id string) error {
				if got != ctx || id != "operation-1" {
					t.Error("requestAbort arguments changed")
				}
				calls = append(calls, "requestAbort")
				return nil
			},
			queue: func(got context.Context, text string, images []ai.ImageContent) (string, error) {
				if got != ctx || images != nil {
					t.Error("queue arguments changed")
				}
				calls = append(calls, text)
				switch text {
				case "later":
					return "queue-1", nil
				case "after":
					return "queue-2", nil
				default:
					t.Errorf("unexpected queue message: %q", text)
					return "", nil
				}
			},
		}
		// Go uses a typed token constructed from the production service ID; types and values cannot both be named AgentController.
		definition := pico3.DefineService[AgentController](AgentControllerID)
		host, err := chord.CreateFacetHost(ctx, chord.FacetOptions{Facets: []chord.Facet{{Id: "test-agent-controller", Setup: func(env *chord.FacetEnvironment) error {
			return chord.ProvideService[AgentController](env, definition, CreateAgentController(lane))
		}}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := host.Dispose(context.Background()); err != nil {
				t.Error(err)
			}
		})
		if got := host.Services().Catalogue(); !reflect.DeepEqual(got, []chord.ServiceCatalogueEntry{{ServiceId: "pi.agent-controller", Mode: chord.ServiceSingleton}}) {
			t.Fatalf("catalogue = %#v", got)
		}
		for _, call := range []struct{ member, arg, want string }{
			{"prompt", `{"message":"hello","images":null}`, `{"accepted":true,"operationId":"operation-1","error":null}`},
			{"requestAbort", `"operation-1"`, ""},
			{"steer", `{"message":"later","images":null}`, `{"accepted":true,"entryId":"queue-1","error":null}`},
			{"followUp", `{"message":"after","images":null}`, `{"accepted":true,"entryId":"queue-2","error":null}`},
		} {
			got, err := host.Services().Invoke(ctx, chord.ServiceCall{ServiceId: AgentControllerID, Member: call.member, Args: []json.RawMessage{json.RawMessage(call.arg)}})
			if err != nil || string(got) != call.want {
				t.Fatalf("%s = %s, %v, want %s", call.member, got, err, call.want)
			}
		}
		if !reflect.DeepEqual(calls, []string{"prompt", "requestAbort", "later", "after"}) || !reflect.DeepEqual(lane.queueCalls, []string{"steer", "followUp"}) {
			t.Fatalf("calls = %v, queues = %v", calls, lane.queueCalls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/experimental-agent-controller.test.ts:95
	t.Run("wraps queue, resume, compaction, and navigation lane operations", func(t *testing.T) {
		ctx := t.Context()
		var calls []string
		lane := &controllerLane{
			queue: func(got context.Context, text string, images []ai.ImageContent) (string, error) {
				if got != ctx || text != "next" || images != nil {
					t.Error("nextRun arguments changed")
				}
				calls = append(calls, "nextRun")
				return "queue-3", nil
			},
			cancel: func(got context.Context, id string) (string, error) {
				if got != ctx || id != "queue-3" {
					t.Error("cancelQueued arguments changed")
				}
				calls = append(calls, "cancelQueued")
				return "cancelled", nil
			},
			resume: func(got context.Context) (LaneOperation, error) {
				if got != ctx {
					t.Error("resume context changed")
				}
				calls = append(calls, "resume")
				return LaneOperation{OperationID: "operation-1", Status: "completed"}, nil
			},
			compact: func(got context.Context, opts *LaneCompactionOptions) (LaneOperation, error) {
				if got != ctx || !reflect.DeepEqual(opts, &LaneCompactionOptions{CustomInstructions: new("short")}) {
					t.Errorf("compact arguments changed: %#v", opts)
				}
				calls = append(calls, "compact")
				return LaneOperation{OperationID: "compact-1", Status: "completed"}, nil
			},
			navigate: func(got context.Context, id *string, opts LaneNavigateOptions) (LaneOperation, error) {
				if got != ctx || id == nil || *id != "entry-1" || !reflect.DeepEqual(opts, LaneNavigateOptions{Summarize: true, Label: new("branch")}) {
					t.Errorf("navigate arguments changed: %v %#v", id, opts)
				}
				calls = append(calls, "navigateTree")
				return LaneOperation{OperationID: "navigation-1", Status: "completed"}, nil
			},
		}
		controller := CreateAgentController(lane)
		queue, err := controller.NextRun(ctx, AgentPromptRequest{Message: "next"})
		if err != nil || !reflect.DeepEqual(queue, AgentQueueResponse{Accepted: true, EntryID: new("queue-3")}) {
			t.Fatalf("nextRun = %#v, %v", queue, err)
		}
		cancel, err := controller.CancelQueued(ctx, "queue-3")
		if err != nil || cancel.Outcome != "cancelled" {
			t.Fatalf("cancelQueued = %#v, %v", cancel, err)
		}
		resume, err := controller.Resume(ctx)
		if err != nil || resume.OperationID == nil || *resume.OperationID != "operation-1" {
			t.Fatalf("resume = %#v, %v", resume, err)
		}
		compact, err := controller.Compact(ctx, AgentCompactionRequest{CustomInstructions: new("short")})
		if err != nil || compact.OperationID == nil || *compact.OperationID != "compact-1" {
			t.Fatalf("compact = %#v, %v", compact, err)
		}
		navigate, err := controller.Navigate(ctx, AgentNavigationRequest{TargetID: new("entry-1"), Summarize: true, Label: new("branch")})
		if err != nil || navigate.OperationID == nil || *navigate.OperationID != "navigation-1" {
			t.Fatalf("navigate = %#v, %v", navigate, err)
		}
		if !reflect.DeepEqual(calls, []string{"nextRun", "cancelQueued", "resume", "compact", "navigateTree"}) {
			t.Fatalf("calls = %v", calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/experimental-agent-controller.test.ts:140
	t.Run("reports accepted successful and failed operations", func(t *testing.T) {
		for _, operation := range []LaneOperation{
			{OperationID: "operation-1", Status: "completed"},
			{OperationID: "operation-2", Status: "failed", Error: &AgentOperationError{Code: "provider", Message: "failed"}},
		} {
			controller := CreateAgentController(&controllerLane{operation: func(context.Context, string, []ai.ImageContent) (LaneOperation, error) { return operation, nil }})
			got, err := controller.Prompt(t.Context(), AgentPromptRequest{Message: "hello"})
			want := AgentOperationResponse{Accepted: true, OperationID: new(operation.OperationID), Error: operation.Error}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("prompt = %#v, %v, want %#v", got, err, want)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/experimental-agent-controller.test.ts:168; table rows :17-29.
	for _, tt := range []struct {
		name string
		err  error
		id   *string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/experimental-agent-controller.test.ts:17 — LaneBusy table row.
		{"lane_busy", &harness.LaneBusy{Lane: "main", OperationID: "operation-1", OperationKind: "run", Message: "busy"}, new("operation-1")},
		// .upstream/v0.87.1/packages/coding-agent/test/experimental-agent-controller.test.ts:26 — InvalidMessage table row.
		{"invalid_message", &harness.InvalidMessage{Lane: "main", Reason: "invalid", Message: "invalid"}, nil},
		// .upstream/v0.87.1/packages/coding-agent/test/experimental-agent-controller.test.ts:27 — UnknownSkill table row.
		{"unknown_skill", &harness.UnknownSkill{Name: "skill", Message: "unknown"}, nil},
		// .upstream/v0.87.1/packages/coding-agent/test/experimental-agent-controller.test.ts:28 — UnknownTemplate table row.
		{"unknown_template", &harness.UnknownTemplate{Name: "template", Message: "unknown"}, nil},
		// .upstream/v0.87.1/packages/coding-agent/test/experimental-agent-controller.test.ts:29 — Closed table row.
		{"closed", &harness.Closed{Message: "closed"}, nil},
	} {
		t.Run("maps admission error to a stable response/"+tt.name, func(t *testing.T) {
			controller := CreateAgentController(&controllerLane{operation: func(context.Context, string, []ai.ImageContent) (LaneOperation, error) {
				return LaneOperation{}, tt.err
			}})
			got, err := controller.Prompt(t.Context(), AgentPromptRequest{Message: "hello"})
			want := AgentOperationResponse{OperationID: tt.id, Error: &AgentOperationError{Code: tt.name, Message: tt.err.Error()}}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("prompt = %#v, %v, want %#v", got, err, want)
			}
		})
	}
}
