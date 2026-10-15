package inproc

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
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
	result, err := runner.EmitBoundary(context.Background(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Outcome: extension.AgentActivityCompleted}},
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
	result, err := runner.EmitBoundary(context.Background(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Outcome: extension.AgentActivityCompleted}},
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

// A typed API.OnTurnEnd handler returns the TurnEndEventResult (types.ts: ExtensionHandler<TurnEndEvent, TurnEndEventResult>),
// and the runner chains it exactly as it chains agent_before_settle results.
// Pi: packages/coding-agent/src/core/extensions/types.ts:1558 (API.on).
func TestTypedOnTurnEndHandlerResultChainsThroughEmitBoundary(t *testing.T) {
	var api extension.API = &extensiontest.Fake{}
	fake := api.(*extensiontest.Fake)
	api.OnTurnEnd(func(_ context.Context, evt extension.TurnEndEvent) (extension.TurnEndEventResult, error) {
		draft := append(slices.Clone(evt.Entries), extension.SessionBoundaryDraft{Type: "custom", CustomType: "typed"})
		proceed := true
		return extension.TurnEndEventResult{Entries: &draft, Continue: &proceed}, nil
	})
	handler := fake.OnTurnEndHandlers[0]
	ext := extension.Extension{Path: "typed", Handlers: map[string][]extension.HandlerFn{
		"turn_end": {func(args ...any) (any, error) {
			return handler(context.Background(), args[0].(extension.TurnEndEvent))
		}},
	}}
	runner := NewRunner([]extension.Extension{ext}, t.TempDir())
	result, err := runner.EmitBoundary(context.Background(), extension.TurnEndEvent{Type: "turn_end", BoundaryState: &extension.BoundaryState{Outcome: extension.AgentActivityCompleted}},
		func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, len(entries))}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || !result.Continue || len(result.Entries) != 1 || result.Entries[0].CustomType != "typed" {
		t.Fatalf("result = %+v, want the typed handler's entry and continuation", result)
	}
}

// A preview built under a boundarywire.Cache leaves its sources as records. Each handler that reads Go values receives them decoded, in its own preview, as Pi hands every handler entry objects (runner.ts emitBoundary); a handler that reads only JSON receives the preview undecoded, and a dispatch of such handlers decodes nothing.
func TestEmitBoundaryDecodesSourcesOnlyForHandlersThatReadGoValues(t *testing.T) {
	record := json.RawMessage(`{"type":"custom","id":"e1","parentId":null,"timestamp":"t","customType":"state","data":{"z":1,"a":2}}`)
	type seen struct {
		path   string
		source map[string]any
	}
	mapOrNil := func(source any) map[string]any {
		if source == nil {
			return nil
		}
		return source.(map[string]any)
	}
	var observed []seen
	handler := func(path string) extension.HandlerFn {
		return func(args ...any) (any, error) {
			event := args[0].(*extension.AgentBeforeSettleEvent)
			// The source as the handler received it; the handler then changes it, which no later handler sees.
			observed = append(observed, seen{path, maps.Clone(mapOrNil(event.Context.ContextEntries[0].SourceEntry))})
			if source, ok := event.Context.ContextEntries[0].SourceEntry.(map[string]any); ok {
				source["customType"] = "changed by " + path
			}
			return nil, nil
		}
	}
	dispatch := func(t *testing.T, kinds ...string) []seen {
		t.Helper()
		observed = nil
		extensions := make([]extension.Extension, len(kinds))
		for i, kind := range kinds {
			extensions[i] = extension.Extension{Path: kind + string(rune('0'+i))}
			if kind == "json" {
				extensions[i].AddJSONEventHandler("agent_before_settle", 1, handler(extensions[i].Path))
			} else {
				extensions[i].AddEventHandler("agent_before_settle", 1, handler(extensions[i].Path))
			}
		}
		ctx, wire := boundarywire.WithCache(t.Context())
		builds := 0
		_, err := NewRunner(extensions, t.TempDir()).EmitBoundary(ctx, &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Outcome: extension.AgentActivityCompleted}},
			func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
				builds++
				preview := extension.BoundaryContextPreview{ContextEntries: []extension.ProjectedSessionEntry{{Messages: []extension.AgentMessage{}}}}
				wire.Built(preview, boundarywire.Key{Session: t, Revision: uint64(builds)}, true, []json.RawMessage{record})
				return preview, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		return observed
	}
	decoded := func(string) map[string]any {
		return map[string]any{"type": "custom", "id": "e1", "parentId": nil, "timestamp": "t", "customType": "state", "data": map[string]any{"z": 1.0, "a": 2.0}}
	}
	got := dispatch(t, "go", "json", "go", "json")
	want := []seen{{"go0", decoded("go0")}, {"json1", map[string]any(nil)}, {"go2", decoded("go2")}, {"json3", map[string]any(nil)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("handlers observed %v, want %v", got, want)
	}
	if got := dispatch(t, "json", "json"); !reflect.DeepEqual(got, []seen{{"json0", map[string]any(nil)}, {"json1", map[string]any(nil)}}) {
		t.Fatalf("JSON handlers observed %v, want undecoded previews", got)
	}
}
