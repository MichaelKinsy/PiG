package coding

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// Pi agent-session.ts:1290-1296 and :1192-1301 emit turn_start with the session's turn counter and Date.now(), reset to 0 at agent_start and
// advanced after each turn_end: a run with a tool call is turns 0 and 1, and the next prompt starts again at 0.
func TestSessionEmitsTurnStartWithThePerRunTurnIndexAndATimestamp(t *testing.T) {
	var starts []extension.TurnStartEvent
	var ends []int
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"turn_start": {func(args ...any) (any, error) {
			starts = append(starts, args[0].(extension.TurnStartEvent))
			return nil, nil
		}},
		"turn_end": {func(args ...any) (any, error) {
			ends = append(ends, args[0].(extension.TurnEndEvent).TurnIndex)
			return nil, nil
		}},
	}}
	tool := boundaryTool{name: "noop", label: "Noop", description: "Noop", text: "done"}
	h := newBoundaryHarness(t, harnessOptions{extension: ext, tools: []agent.AgentTool{tool}},
		boundaryToolReply("noop", ai.JsonObject{}, ai.StopReasonToolUse),
		boundaryReply("first done", ai.StopReasonStop, 0),
		boundaryReply("second done", ai.StopReasonStop, 0))

	before := time.Now().UnixMilli()
	boundaryPrompt(t, h, "first")
	after := time.Now().UnixMilli()
	boundaryPrompt(t, h, "second")

	var indexes []int
	for _, event := range starts {
		indexes = append(indexes, event.TurnIndex)
		if event.Type != "turn_start" {
			t.Fatalf("turn_start event type = %q", event.Type)
		}
	}
	if want := []int{0, 1, 0}; !slices.Equal(indexes, want) {
		t.Fatalf("turn_start indexes = %v, want %v (two turns in the tool run, then a new run from 0)", indexes, want)
	}
	if want := []int{0, 1, 0}; !slices.Equal(ends, want) {
		t.Fatalf("turn_end indexes = %v, want %v", ends, want)
	}
	if starts[0].Timestamp < before || starts[1].Timestamp > after {
		t.Fatalf("turn_start timestamps %d, %d are outside the run's window [%d, %d] (Date.now())", starts[0].Timestamp, starts[1].Timestamp, before, after)
	}
}

// Pi agent-session.ts:2610-2633 (setThinkingLevel) emits thinking_level_select only when the clamped effective level differs from the
// current one, carrying the effective level and the previous level.
func TestSessionEmitsThinkingLevelSelectOnlyForAChangeWithTheEffectiveLevel(t *testing.T) {
	var events []extension.ThinkingLevelSelectEvent
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"thinking_level_select": {func(args ...any) (any, error) {
			events = append(events, args[0].(extension.ThinkingLevelSelectEvent))
			return nil, nil
		}},
	}}
	h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
	model := *h.session.Model()
	model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
	if err := h.session.SetModel(&model); err != nil {
		t.Fatal(err)
	}
	supported := ai.GetSupportedThinkingLevels(&model)
	if !slices.Contains(supported, ai.ThinkingHigh) || slices.Contains(supported, ai.ThinkingXHigh) {
		t.Fatalf("test model supports %v, want high but not xhigh", supported)
	}
	start := h.session.ThinkingLevel()
	set := func(level ai.ModelThinkingLevel) {
		t.Helper()
		if err := h.session.SetThinkingLevel(level); err != nil {
			t.Fatal(err)
		}
		if err := h.session.FlushEvents(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	set(ai.ThinkingHigh)
	set(ai.ThinkingHigh)
	set(ai.ThinkingXHigh)
	if want := []extension.ThinkingLevelSelectEvent{{Type: "thinking_level_select", Level: ai.ThinkingHigh, PreviousLevel: start}}; !slices.Equal(events, want) {
		t.Fatalf("thinking_level_select events = %+v, want only the first change %+v (an unchanged or clamped-to-current level emits nothing)", events, want)
	}
	set(ai.ThinkingLow)
	if len(events) != 2 || events[1].Level != ai.ThinkingLow || events[1].PreviousLevel != ai.ThinkingHigh {
		t.Fatalf("thinking_level_select events = %+v, want a second event high -> low", events)
	}
	set(ai.ThinkingXHigh)
	if len(events) != 3 || events[2].Level != ai.ThinkingHigh || events[2].PreviousLevel != ai.ThinkingLow {
		t.Fatalf("thinking_level_select events = %+v, want a third event low -> high: the event carries the clamped effective level, not the requested xhigh", events)
	}
}

// Pi agent-session.ts:2629 emits thinking_level_select with `void`, so setThinkingLevel returns once the first handler's synchronous
// prefix ran (runner.ts emit awaits each handler) and never waits for a suspended handler. The Session cancels and drains the
// suspended handler on Close.
func TestSessionThinkingLevelSelectDoesNotAwaitSuspendedHandlers(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"thinking_level_select": {func(args ...any) (any, error) {
			ctx := args[1].(context.Context)
			close(started)
			invocation.Acknowledge(ctx)
			<-ctx.Done()
			close(stopped)
			return nil, nil
		}},
	}}
	h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
	model := *h.session.Model()
	model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
	if err := h.session.SetModel(&model); err != nil {
		t.Fatal(err)
	}
	level := ai.ThinkingHigh
	if h.session.ThinkingLevel() == level {
		level = ai.ThinkingLow
	}
	done := make(chan error, 1)
	go func() { done <- h.session.SetThinkingLevel(level) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SetThinkingLevel waited for a suspended thinking_level_select handler")
	}
	select {
	case <-started:
	default:
		t.Fatal("SetThinkingLevel returned before the handler's synchronous prefix")
	}
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Close did not drain the cancelled thinking_level_select handler")
	}
}

// Pi runner.ts emit awaits the first handler before invoking the second, and setThinkingLevel does not await the emit: the call returns
// after the first handler and before a later handler finishes.
func TestSessionThinkingLevelSelectDoesNotAwaitLaterHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release, first := make(chan struct{}), make(chan struct{})
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"thinking_level_select": {
				func(...any) (any, error) { close(first); return nil, nil },
				func(...any) (any, error) { <-release; return nil, nil },
			},
		}}
		h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
		model := *h.session.Model()
		model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
		if err := h.session.SetModel(&model); err != nil {
			t.Fatal(err)
		}
		level := ai.ThinkingHigh
		if h.session.ThinkingLevel() == level {
			level = ai.ThinkingLow
		}
		done := make(chan error, 1)
		go func() { done <- h.session.SetThinkingLevel(level) }()
		synctest.Wait()
		select {
		case <-first:
		default:
			t.Error("the first thinking_level_select handler did not run")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		default:
			t.Error("SetThinkingLevel awaited a later thinking_level_select handler")
		}
		close(release)
	})
}

// SetThinkingLevelOnMain mutates on the dispatcher and notifies on the caller, as SetModelOnMain does: a rejected mutation changes nothing and
// emits nothing, and an accepted one emits thinking_level_select with the clamped level after the dispatcher returned.
// mutation-checked: notifying inside the mutation, or ignoring a dispatcher error, fails it.
func TestSessionSetThinkingLevelOnMainDispatchesOnlyTheMutation(t *testing.T) {
	var events []extension.ThinkingLevelSelectEvent
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"thinking_level_select": {func(args ...any) (any, error) {
			events = append(events, args[0].(extension.ThinkingLevelSelectEvent))
			return nil, nil
		}},
	}}
	h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
	model := *h.session.Model()
	model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
	if err := h.session.SetModel(&model); err != nil {
		t.Fatal(err)
	}
	var mutated bool
	err := h.session.SetThinkingLevelOnMain(ai.ThinkingXHigh, ModelMutationOptions{}, func(mutate func() error) error {
		if err := mutate(); err != nil {
			return err
		}
		mutated = true
		if err := h.session.FlushEvents(t.Context()); err != nil || len(events) != 0 {
			t.Fatalf("the notification ran inside the mutation (%v, %v)", events, err)
		}
		return nil
	})
	if err != nil || !mutated {
		t.Fatalf("SetThinkingLevelOnMain = %v (mutated %v)", err, mutated)
	}
	if err := h.session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Level != ai.ThinkingHigh {
		t.Fatalf("events = %+v, want one event with the clamped level high", events)
	}
	rejected := errors.New("superseded")
	if err := h.session.SetThinkingLevelOnMain(ai.ThinkingLow, ModelMutationOptions{}, func(func() error) error { return rejected }); !errors.Is(err, rejected) {
		t.Fatalf("a rejected dispatch returned %v", err)
	}
	if err := h.session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.session.ThinkingLevel() != ai.ThinkingHigh || len(events) != 1 {
		t.Fatalf("a rejected dispatch changed the level to %q or emitted %v", h.session.ThinkingLevel(), events)
	}
}
