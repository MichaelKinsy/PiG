package services

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

type controllerLane struct {
	queueCalls []string
	operation  func(context.Context, string, []ai.ImageContent) (LaneOperation, error)
	queue      func(context.Context, string, []ai.ImageContent) (string, error)
	abort      func(context.Context, string) error
	cancel     func(context.Context, string) (string, error)
	resume     func(context.Context) (LaneOperation, error)
	compact    func(context.Context, *LaneCompactionOptions) (LaneOperation, error)
	navigate   func(context.Context, *string, LaneNavigateOptions) (LaneOperation, error)
}

func (l *controllerLane) Prompt(ctx context.Context, message string, images []ai.ImageContent) (LaneOperation, error) {
	return l.operation(ctx, message, images)
}
func (l *controllerLane) Steer(ctx context.Context, message string, images []ai.ImageContent) (string, error) {
	l.queueCalls = append(l.queueCalls, "steer")
	return l.queue(ctx, message, images)
}
func (l *controllerLane) FollowUp(ctx context.Context, message string, images []ai.ImageContent) (string, error) {
	l.queueCalls = append(l.queueCalls, "followUp")
	return l.queue(ctx, message, images)
}
func (l *controllerLane) NextRun(ctx context.Context, message string, images []ai.ImageContent) (string, error) {
	l.queueCalls = append(l.queueCalls, "nextRun")
	return l.queue(ctx, message, images)
}
func (l *controllerLane) RequestAbort(ctx context.Context, id string) error { return l.abort(ctx, id) }
func (l *controllerLane) CancelQueued(ctx context.Context, id string) (string, error) {
	return l.cancel(ctx, id)
}
func (l *controllerLane) Resume(ctx context.Context) (LaneOperation, error) { return l.resume(ctx) }
func (l *controllerLane) Compact(ctx context.Context, options *LaneCompactionOptions) (LaneOperation, error) {
	return l.compact(ctx, options)
}
func (l *controllerLane) NavigateTree(ctx context.Context, id *string, options LaneNavigateOptions) (LaneOperation, error) {
	return l.navigate(ctx, id, options)
}

func TestControllerMapsAdmissionAndTerminalResults(t *testing.T) {
	cases := []struct {
		err  error
		code string
		id   *string
	}{
		{&harness.LaneBusy{OperationID: "busy", Message: "busy"}, "lane_busy", new("busy")},
		{&harness.InvalidMessage{Message: "invalid"}, "invalid_message", nil},
		{&harness.UnknownSkill{Message: "unknown skill"}, "unknown_skill", nil},
		{&harness.UnknownTemplate{Message: "unknown template"}, "unknown_template", nil},
		{&harness.NothingToCompact{Message: "empty"}, "nothing_to_compact", nil},
		{&harness.NothingToResume{Message: "idle"}, "nothing_to_resume", nil},
		{&harness.InvalidNavigation{Message: "invalid target"}, "invalid_navigation", nil},
		{&harness.UnknownTarget{Message: "unknown"}, "unknown_target", nil},
		{&harness.Closed{Message: "closed"}, "closed", nil},
		{&harness.NoActiveOperation{Message: "inactive"}, "operation_failed", nil},
	}
	for _, tt := range cases {
		t.Run(tt.code, func(t *testing.T) {
			controller := CreateAgentController(&controllerLane{operation: func(context.Context, string, []ai.ImageContent) (LaneOperation, error) {
				return LaneOperation{}, tt.err
			}})
			got, err := controller.Prompt(t.Context(), AgentPromptRequest{Message: "hi"})
			want := AgentOperationResponse{OperationID: tt.id, Error: &AgentOperationError{Code: tt.code, Message: tt.err.Error()}}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("response = %#v, %v; want %#v", got, err, want)
			}
		})
	}
	for _, status := range []string{"completed", "declined", "aborted", "failed", "suspended"} {
		t.Run(status, func(t *testing.T) {
			failure := &AgentOperationError{Code: "provider", Message: "failed"}
			controller := CreateAgentController(&controllerLane{operation: func(context.Context, string, []ai.ImageContent) (LaneOperation, error) {
				return LaneOperation{OperationID: "op", Status: status, Error: failure}, nil
			}})
			got, err := controller.Prompt(t.Context(), AgentPromptRequest{})
			want := AgentOperationResponse{Accepted: true, OperationID: new("op")}
			if status == "failed" {
				want.Error = failure
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("response = %#v, %v; want %#v", got, err, want)
			}
		})
	}
}

func TestControllerDelegatesAllCommandsAndPreservesNullAndEmpty(t *testing.T) {
	ctx := t.Context()
	for _, images := range [][]AgentPromptImage{nil, {}, {{Type: "image", Data: "AA==", MimeType: "image/png"}}} {
		lane := &controllerLane{}
		check := func(got context.Context, message string, received []ai.ImageContent) {
			t.Helper()
			if got != ctx || message != "hello" || len(received) != len(images) || (received == nil) != (images == nil) {
				t.Fatalf("prompt = %q, %#v, context matches=%v", message, received, got == ctx)
			}
			if len(images) != 0 && (received[0].Data != images[0].Data || received[0].MimeType != images[0].MimeType) {
				t.Fatal("image changed")
			}
		}
		lane.operation = func(got context.Context, message string, received []ai.ImageContent) (LaneOperation, error) {
			check(got, message, received)
			return LaneOperation{OperationID: "op"}, nil
		}
		lane.queue = func(got context.Context, message string, received []ai.ImageContent) (string, error) {
			check(got, message, received)
			return "entry", nil
		}
		controller := CreateAgentController(lane)
		request := AgentPromptRequest{Message: "hello", Images: images}
		if _, err := controller.Prompt(ctx, request); err != nil {
			t.Fatal(err)
		}
		for _, queue := range []func(context.Context, AgentPromptRequest) (AgentQueueResponse, error){controller.Steer, controller.FollowUp, controller.NextRun} {
			got, err := queue(ctx, request)
			if err != nil || !reflect.DeepEqual(got, AgentQueueResponse{Accepted: true, EntryID: new("entry")}) {
				t.Fatalf("queue = %#v, %v", got, err)
			}
		}
		if !reflect.DeepEqual(lane.queueCalls, []string{"steer", "followUp", "nextRun"}) {
			t.Fatalf("queue dispatch = %v", lane.queueCalls)
		}
	}
	for _, instructions := range []*string{nil, new(""), new("short")} {
		lane := &controllerLane{compact: func(got context.Context, options *LaneCompactionOptions) (LaneOperation, error) {
			if got != ctx || (options == nil) != (instructions == nil) || options != nil && !reflect.DeepEqual(options.CustomInstructions, instructions) {
				t.Fatalf("compact = %#v", options)
			}
			return LaneOperation{OperationID: "compact"}, nil
		}, navigate: func(got context.Context, id *string, options LaneNavigateOptions) (LaneOperation, error) {
			if got != ctx || id != nil || !options.Summarize || !reflect.DeepEqual(options.Label, instructions) || !reflect.DeepEqual(options.CustomInstructions, instructions) {
				t.Fatalf("navigate = %#v, %#v", id, options)
			}
			return LaneOperation{OperationID: "navigate"}, nil
		}}
		controller := CreateAgentController(lane)
		got, err := controller.Compact(ctx, AgentCompactionRequest{CustomInstructions: instructions})
		if err != nil || got.OperationID == nil || *got.OperationID != "compact" {
			t.Fatalf("compact = %#v, %v", got, err)
		}
		got, err = controller.Navigate(ctx, AgentNavigationRequest{Summarize: true, Label: instructions, CustomInstructions: instructions})
		if err != nil || got.OperationID == nil || *got.OperationID != "navigate" {
			t.Fatalf("navigate = %#v, %v", got, err)
		}
	}
}

func TestControllerWaitsForLaneCompletion(t *testing.T) {
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	controller := CreateAgentController(&controllerLane{operation: func(received context.Context, _ string, _ []ai.ImageContent) (LaneOperation, error) {
		close(started)
		<-received.Done()
		return LaneOperation{}, received.Err()
	}})
	done := make(chan error, 1)
	go func() {
		_, err := controller.Prompt(ctx, AgentPromptRequest{})
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("prompt returned before the lane settled: %v", err)
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancellation = %v", err)
	}
}

func TestControllerRejectionsAndCancellation(t *testing.T) {
	thrown := errors.New("transport failed")
	lane := &controllerLane{
		operation: func(ctx context.Context, _ string, _ []ai.ImageContent) (LaneOperation, error) {
			<-ctx.Done()
			return LaneOperation{}, ctx.Err()
		},
		queue: func(context.Context, string, []ai.ImageContent) (string, error) {
			return "", &harness.Closed{Message: "closed"}
		},
		abort:  func(context.Context, string) error { return &harness.OperationMismatch{Message: "mismatch"} },
		cancel: func(context.Context, string) (string, error) { return "", thrown },
		resume: func(context.Context) (LaneOperation, error) {
			return LaneOperation{}, &harness.LaneBusy{OperationID: "hidden", Message: "busy"}
		},
	}
	controller := CreateAgentController(lane)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := controller.Prompt(ctx, AgentPromptRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	if _, err := controller.CancelQueued(t.Context(), "entry"); !errors.Is(err, thrown) {
		t.Fatalf("throw = %v", err)
	}
	if err := controller.RequestAbort(t.Context(), "op"); err == nil || err.Error() != "mismatch" {
		t.Fatalf("abort = %v", err)
	} else if _, ok := errors.AsType[harness.TaggedError](err); ok {
		t.Fatal("abort leaked tagged lane error")
	}
	got, err := controller.Resume(t.Context())
	if err != nil || got.OperationID != nil || got.Accepted || got.Error.Code != "lane_busy" {
		t.Fatalf("resume = %#v, %v", got, err)
	}
	for _, queue := range []func(context.Context, AgentPromptRequest) (AgentQueueResponse, error){controller.Steer, controller.FollowUp, controller.NextRun} {
		got, err := queue(t.Context(), AgentPromptRequest{})
		if err != nil || got.Accepted || got.EntryID != nil || got.Error.Code != "closed" {
			t.Fatalf("queue = %#v, %v", got, err)
		}
	}
	lane.abort = func(ctx context.Context, id string) error {
		if ctx != t.Context() || id != "op" {
			t.Fatal("abort arguments changed")
		}
		return nil
	}
	if err := controller.RequestAbort(t.Context(), "op"); err != nil {
		t.Fatal(err)
	}
	lane.resume = func(ctx context.Context) (LaneOperation, error) {
		if ctx != t.Context() {
			t.Fatal("resume context changed")
		}
		return LaneOperation{OperationID: "resumed", Status: "suspended"}, nil
	}
	resumed, err := controller.Resume(t.Context())
	if err != nil || !reflect.DeepEqual(resumed, AgentOperationResponse{Accepted: true, OperationID: new("resumed")}) {
		t.Fatalf("resume = %#v, %v", resumed, err)
	}
	for _, outcome := range []string{"cancelled", "already_consumed", "not_found"} {
		lane.cancel = func(ctx context.Context, id string) (string, error) {
			if ctx != t.Context() || id != "entry" {
				t.Fatal("cancel arguments changed")
			}
			return outcome, nil
		}
		got, err := controller.CancelQueued(t.Context(), "entry")
		if err != nil || got.Outcome != outcome {
			t.Fatalf("cancel = %#v, %v", got, err)
		}
	}
}
