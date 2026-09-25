package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestModelsFailuresDoNotPublishConfiguration(t *testing.T) {
	failure := errors.New("injected failure")
	for _, mode := range []string{"set model", "set thinking", "settings", "flush", "state", "read", "no runtime"} {
		t.Run(mode, func(t *testing.T) {
			model := testModel("local", "chosen", true)
			lane := &testModelsLane{models: []*ai.Model{model}, model: model, thinking: ai.ThinkingOff}
			models := &testModelsRuntime{all: lane.models}
			settings := &testModelsSettings{flush: func() error { return nil }}
			runtime, state := testService(lane, models, settings)
			switch mode {
			case "set model", "set thinking":
				lane.setError = failure
			case "settings":
				settings.setError = failure
			case "flush":
				settings.flush = func() error { return failure }
			case "state":
				state.changeError = failure
			case "read":
				lane.getModel = func(context.Context) (*ai.Model, error) { return nil, failure }
			case "no runtime":
				runtime, state = testService(lane, nil, settings)
			}
			var err error
			if mode == "set thinking" {
				err = runtime.Service.SelectThinking(t.Context(), ai.ThinkingHigh)
			} else {
				err = runtime.Service.Select(t.Context(), ModelRef{"local", "chosen"})
			}
			if mode == "no runtime" {
				if err == nil || err.Error() != "Unknown model: local/chosen" {
					t.Fatal(err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatal(err)
			}
			checkModelsEqual(t, state.Value().Configuration.Model, (*ModelRef)(nil))
			checkModelsEqual(t, len(state.changes), 0)
		})
	}
}

func TestModelsRefreshPublicationFailureDoesNotStartRuntime(t *testing.T) {
	called := false
	models := &testModelsRuntime{refresh: func(context.Context) (ModelsRefreshResult, error) { called = true; return ModelsRefreshResult{}, nil }}
	runtime, state := testService(&testModelsLane{}, models, nil)
	failure := errors.New("publication failed")
	state.changeError = failure
	if err := runtime.Service.Refresh(t.Context()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if called {
		t.Fatal("refresh started after publication rejected")
	}
	checkModelsEqual(t, state.Value().Refresh.Status, "idle")
}

func TestModelsConcurrentCatalogRevisionsAreDistinct(t *testing.T) {
	runtime, state := testService(&testModelsLane{thinking: ai.ThinkingOff}, nil, nil)
	var work sync.WaitGroup
	const operations = 100
	for range operations {
		work.Go(func() {
			if err := runtime.Activate(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	work.Wait()
	revisions := make(map[int]bool)
	for _, value := range state.changes {
		revisions[value.Catalog.Revision] = true
	}
	for revision := 1; revision <= operations; revision++ {
		if !revisions[revision] {
			t.Fatalf("missing revision %d", revision)
		}
	}
}

func TestModelsSelectionPersistsWithSettingsManager(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	settings := icodingagent.NewSettingsManager(cwd, agentDir)
	model := testModel("local", "chosen", true)
	lane := &testModelsLane{models: []*ai.Model{model}, thinking: ai.ThinkingLow}
	runtime, _ := testService(lane, &testModelsRuntime{all: lane.models}, settings)
	requireModelsOK(t, runtime.Service.Select(t.Context(), ModelRef{"local", "chosen"}))
	stored := icodingagent.NewSettingsManager(cwd, agentDir).Get()
	checkModelsEqual(t, stored.DefaultProvider, "local")
	checkModelsEqual(t, stored.DefaultModel, "chosen")
	requireModelsOK(t, runtime.Service.SelectThinking(t.Context(), ai.ThinkingHigh))
	checkModelsEqual(t, icodingagent.NewSettingsManager(cwd, agentDir).Get().DefaultThinkingLevel, stored.DefaultThinkingLevel)
}

func TestModelsPassCancelledContextToRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	models := &testModelsRuntime{refresh: func(received context.Context) (ModelsRefreshResult, error) {
		if received != ctx {
			return ModelsRefreshResult{}, errors.New("wrong refresh context")
		}
		return ModelsRefreshResult{}, received.Err()
	}}
	runtime, state := testService(&testModelsLane{}, models, nil)
	if err := runtime.Service.Refresh(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	checkModelsEqual(t, state.Value().Refresh.Status, "refreshing")
}

func BenchmarkModelsActivateCatalog(b *testing.B) {
	for _, size := range []int{0, 1000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			models := &testModelsRuntime{available: make([]*ai.Model, size)}
			for i := range models.available {
				models.available[i] = testModel("local", fmt.Sprint(i), true)
			}
			runtime, state := testService(&testModelsLane{thinking: ai.ThinkingHigh}, models, nil)
			b.ReportAllocs()
			for b.Loop() {
				if err := runtime.Activate(b.Context()); err != nil {
					b.Fatal(err)
				}
				state.changes = nil
			}
		})
	}
}
