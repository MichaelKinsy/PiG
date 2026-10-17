package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type modelRefreshOracleProbe struct {
	Theme         string            `json:"theme"`
	Width         int               `json:"width"`
	Before        []modelOracleItem `json:"before"`
	After         []modelOracleItem `json:"after"`
	Current       string            `json:"current,omitempty"`
	InitialSearch string            `json:"initialSearch,omitempty"`
	Refresh       string            `json:"refresh"`
	Aborted       bool              `json:"aborted"`
	Errors        []string          `json:"errors"`
	RuntimeError  string            `json:"runtimeError,omitempty"`
	KeysBefore    []string          `json:"keysBefore"`
	KeysAfter     []string          `json:"keysAfter"`
}

// model-selector.ts refreshModels (:184-222) against pinned Pi. The refresh settles with each outcome (success, the runtime's error, one or
// several failed catalogs in failure order, an aborted result without the timeout), or the 15 second timer aborts it, whether the refresh then
// settles or never does. Each probe compares the frame before the refresh, after keys typed while it runs, after it ends, and after keys typed
// afterwards, byte for byte. The timeout rejects refreshModelCatalogs (raceWithAbortSignal), so the picker shows the timeout and keeps the list
// it had: models the aborted refresh added stay hidden.
func TestModelSelectorRefreshModelsMatchesPi(t *testing.T) {
	before := []modelOracleItem{{"anthropic", "claude-a", "Claude A"}, {"openai", "gpt-4o", "GPT 4o"}, {"groq", "llama-3", "Llama 3"}}
	after := append(append([]modelOracleItem{}, before...), modelOracleItem{"openai", "gpt-added", "GPT Added"}, modelOracleItem{"xai", "grok", "Grok"})
	shrunk := before[1:]
	outcomes := []modelRefreshOracleProbe{
		{Refresh: "settle", After: after},
		{Refresh: "settle", After: shrunk},
		{Refresh: "settle", After: after, RuntimeError: "Failed to parse models.json: Unexpected token\n\nFile: /tmp/models.json"},
		{Refresh: "settle", After: after, Errors: []string{"openai"}},
		{Refresh: "settle", After: after, Errors: []string{"xai", "anthropic", "openai"}},
		{Refresh: "settle", After: after, Errors: []string{"openai"}, RuntimeError: "ignored"},
		{Refresh: "settle", After: after, Aborted: true},
		{Refresh: "abort", After: after, Aborted: true},
		{Refresh: "abort", After: after, Aborted: true, Errors: []string{"openai"}},
		{Refresh: "hang", After: after},
	}
	scripts := []struct{ current, search string }{{"", ""}, {"openai/gpt-4o", ""}, {"groq/llama-3", "gpt"}, {"", "grok"}}
	keys := [][2][]string{{nil, nil}, {{"\x1b[B"}, {"\x1b[B", "\x1b[B"}}, {{"g", "p"}, {"\x7f", "\x1b[A"}}}
	var probes []modelRefreshOracleProbe
	for _, theme := range []string{"dark", "light"} {
		for _, outcome := range outcomes {
			for _, script := range scripts {
				for _, k := range keys {
					probe := outcome
					probe.Theme, probe.Width, probe.Before = theme, 100, before
					probe.Current, probe.InitialSearch = script.current, script.search
					probe.KeysBefore, probe.KeysAfter = k[0], k[1]
					if probe.Errors == nil {
						probe.Errors = []string{}
					}
					probes = append(probes, probe)
				}
			}
		}
	}
	// A narrow terminal clips the refresh status and error rows.
	for _, outcome := range outcomes {
		probe := outcome
		probe.Theme, probe.Width, probe.Before = "dark", 24, before
		if probe.Errors == nil {
			probe.Errors = []string{}
		}
		probes = append(probes, probe)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model_selector_refresh.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Frames [][]string `json:"frames"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousBindings, previousTheme, previousCaps, previousTimeout := GetKeybindings(), ActiveTheme(), GetCapabilities(), modelSelectorRefreshTimeout
	t.Cleanup(func() {
		SetKeybindings(previousBindings)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
		modelSelectorRefreshTimeout = previousTimeout
	})
	modelSelectorRefreshTimeout = 20 * time.Millisecond
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	toModels := func(items []modelOracleItem) []*ai.Model {
		out := make([]*ai.Model, 0, len(items))
		for _, item := range items {
			out = append(out, &ai.Model{ID: item.ID, DisplayName: item.Name, ProviderMeta: ai.ProviderMetadata{ProviderID: item.Provider}})
		}
		return out
	}
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		restoreKeybindingsAfterTest(t)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), nil))
		ui, queue := newModelSelectorTUI(t)
		runtime := &fakeModelRuntime{models: toModels(probe.Before), err: probe.RuntimeError}
		settle := func() ai.ModelsRefreshResult {
			runtime.models = toModels(probe.After)
			result := refreshFailing(probe.Errors...)
			result.Aborted = probe.Aborted
			return result
		}
		runtime.refresh = func(ctx context.Context) ai.ModelsRefreshResult {
			switch probe.Refresh {
			case "settle":
				return settle()
			case "abort":
				<-ctx.Done()
				return settle()
			default:
				<-hang
				return ai.ModelsRefreshResult{Aborted: true}
			}
		}
		var current *ai.Model
		if provider, id, ok := strings.Cut(probe.Current, "/"); ok {
			current = runtime.GetModel(provider, id)
		}
		selector := NewModelSelectorComponent(ui, current, runtime, nil, func(*ai.Model) {}, func() {}, probe.InitialSearch, func(*ai.Model) {}, nil)
		frames := [][]string{selector.Render(probe.Width)}
		for _, key := range probe.KeysBefore {
			selector.HandleInput(key)
		}
		frames = append(frames, selector.Render(probe.Width))
		queue.run(t)
		frames = append(frames, selector.Render(probe.Width))
		for _, key := range probe.KeysAfter {
			selector.HandleInput(key)
			frames = append(frames, selector.Render(probe.Width))
		}
		selector.Dispose()
		if want := expected[i].Frames; !reflect.DeepEqual(frames, want) {
			failures++
			if failures <= 6 {
				t.Errorf("probe %d refresh=%s aborted=%v errors=%v runtime=%q current=%q search=%q keys=%q/%q:\n%s", i, probe.Refresh, probe.Aborted, probe.Errors, probe.RuntimeError, probe.Current, probe.InitialSearch, probe.KeysBefore, probe.KeysAfter, modelFrameDifference(frames, want))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
