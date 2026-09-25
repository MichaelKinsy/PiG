package inproc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestEmitBoundaryChainsDraftsContinuationAndPreview(t *testing.T) {
	var observations []extension.BoundaryState
	first := extension.Extension{Path: "first", Handlers: map[string][]extension.HandlerFn{
		"agent_before_settle": {func(args ...any) (any, error) {
			event := args[0].(*extension.AgentBeforeSettleEvent)
			observations = append(observations, event.BoundaryState)
			event.Entries = append(event.Entries, extension.SessionBoundaryDraft{Type: "custom", CustomType: "first"})
			value := true
			return extension.BoundaryResult{Continue: &value}, nil
		}},
	}}
	second := extension.Extension{Path: "second", Handlers: map[string][]extension.HandlerFn{
		"agent_before_settle": {func(args ...any) (any, error) {
			event := args[0].(*extension.AgentBeforeSettleEvent)
			observations = append(observations, event.BoundaryState)
			empty := []extension.SessionBoundaryDraft{}
			return extension.BoundaryResult{Entries: &empty}, nil
		}},
	}}
	runner := NewRunner([]extension.Extension{first, second}, t.TempDir())
	result, err := runner.EmitBoundary(context.Background(), "agent_before_settle", extension.AgentActivityCompleted,
		func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, len(entries))}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(observations))
	}
	if got := []int{len(observations[0].Entries), len(observations[1].Entries)}; !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("entry chain = %v, want [0 1]", got)
	}
	if observations[1].Continue != true || len(observations[1].Context.ContextEntries) != 1 {
		t.Fatalf("second observation = %+v, want chained continuation and preview", observations[1])
	}
	if len(result.Entries) != 0 || !result.Continue || !result.Valid {
		t.Fatalf("result = %+v, want cleared entries with continuation", result)
	}
}

func TestEmitBoundaryReportsInvalidDraftAndLetsLaterHandlerRepair(t *testing.T) {
	first := extension.Extension{Path: "invalid", Handlers: map[string][]extension.HandlerFn{
		"agent_before_settle": {func(...any) (any, error) {
			entries := []extension.SessionBoundaryDraft{{Type: "context_edit", TargetID: "missing"}}
			return extension.BoundaryResult{Entries: &entries}, nil
		}},
	}}
	secondRan := false
	second := extension.Extension{Path: "repair", Handlers: map[string][]extension.HandlerFn{
		"agent_before_settle": {func(args ...any) (any, error) {
			secondRan = true
			event := args[0].(*extension.AgentBeforeSettleEvent)
			if len(event.Entries) != 1 {
				t.Fatalf("repair entries = %d, want 1", len(event.Entries))
			}
			empty := []extension.SessionBoundaryDraft{}
			return extension.BoundaryResult{Entries: &empty}, nil
		}},
	}}
	runner := NewRunner([]extension.Extension{first, second}, t.TempDir())
	var reported []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
	result, err := runner.EmitBoundary(context.Background(), "agent_before_settle", extension.AgentActivityCompleted,
		func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			if len(entries) != 0 {
				return extension.BoundaryContextPreview{}, errors.New("Entry missing not found")
			}
			return extension.BoundaryContextPreview{}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !secondRan || !result.Valid || len(result.Entries) != 0 {
		t.Fatalf("repair result = %+v, secondRan=%t", result, secondRan)
	}
	if !reflect.DeepEqual(reported, []string{"Invalid boundary entries: Entry missing not found"}) {
		t.Fatalf("reported = %v", reported)
	}
}
