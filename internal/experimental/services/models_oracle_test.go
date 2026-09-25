package services

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/ai"
)

// The oracle imports the exact pinned models provider, Chord state, and AI thinking logic. No server or provider connection is created.
func TestModelsProviderMatchesPinnedUpstream(t *testing.T) {
	root, err := filepath.Abs("../../..")
	requireModelsOK(t, err)
	output, err := exec.CommandContext(t.Context(), "node", "--experimental-transform-types", "testdata/models-oracle.mjs", root).Output()
	requireModelsOK(t, err)
	var expected any
	requireModelsOK(t, json.Unmarshal(output, &expected))

	local, other := testModel("local", "chosen", true), testModel("other", "chosen", false)
	lane := &testModelsLane{model: local, models: []*ai.Model{local, other}, thinking: ai.ThinkingHigh}
	failRefresh, warnings := false, true
	models := &testModelsRuntime{available: []*ai.Model{other}, all: lane.models}
	models.refresh = func(_ context.Context) (ModelsRefreshResult, error) {
		if failRefresh {
			return ModelsRefreshResult{}, errors.New("refresh failed")
		}
		if warnings {
			return ModelsRefreshResult{Errors: map[string]error{"local": errors.New("unavailable")}}, nil
		}
		return ModelsRefreshResult{}, nil
	}
	persisted := []any{}
	settings := &testModelsSettings{
		onSet: func(ref ModelRef) { persisted = append(persisted, ref) },
		flush: func() error { persisted = append(persisted, "flushed"); return nil },
	}
	facet := CreateModelsServiceFacet(ModelsServiceFacetOptions{Lane: lane, ModelRuntime: models, SettingsManager: settings})
	var state *testModelsState
	var initial *ModelsState
	runtime := CreateModelsService(lane, models, settings, func(value *ModelsState) pico3.MutableReplicatedStateOf[*ModelsState] {
		initial = copyModelsState(value)
		state = &testModelsState{value: copyModelsState(value)}
		return state
	})
	service := runtime.Service
	requireModelsOK(t, runtime.Activate(context.Background()))
	levels, err := service.GetThinkingLevels(t.Context())
	requireModelsOK(t, err)
	requireModelsOK(t, service.CycleThinking(t.Context()))
	requireModelsOK(t, service.SelectThinking(t.Context(), ai.ThinkingMedium))
	requireModelsOK(t, service.Refresh(t.Context()))
	warnings = false
	requireModelsOK(t, service.Refresh(t.Context()))
	requireModelsOK(t, service.Select(t.Context(), ModelRef{"other", "chosen"}))
	failures := []string{}
	for _, operation := range []func() error{
		func() error { return service.SelectThinking(t.Context(), ai.ThinkingMax) },
		func() error { return service.Select(t.Context(), ModelRef{"missing", "unknown"}) },
		func() error { failRefresh = true; return service.Refresh(t.Context()) },
	} {
		if err := operation(); err != nil {
			failures = append(failures, err.Error())
		} else {
			t.Fatal("expected rejection")
		}
	}
	empty, emptyState := testService(&testModelsLane{thinking: ai.ThinkingOff}, nil, nil)
	requireModelsOK(t, empty.Activate(t.Context()))
	requireModelsOK(t, empty.Service.Refresh(t.Context()))
	actual := map[string]any{
		"facetId":   facet.Id,
		"snapshots": append([]*ModelsState{initial}, state.changes...),
		"levels":    levels,
		"persisted": persisted,
		"failures":  failures,
		"empty":     emptyState.Value(),
	}
	encoded, err := json.Marshal(actual)
	requireModelsOK(t, err)
	var decoded any
	requireModelsOK(t, json.Unmarshal(encoded, &decoded))
	checkModelsEqual(t, decoded, expected)
}
