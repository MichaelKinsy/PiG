package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Boundary obligations of ModelSelectorComponent.HandleInput (packages/coding-agent/src/modes/interactive/components/model-selector.ts
// handleInput) compared with the pinned Pi: the action order tab, up, down, selection page keys, confirm, cancel, save and then the
// search Input when one key is bound to two actions, and every action on an empty, a single-model and a missing-callback selector.

// TestModelSelectorOverlappingBindingsMatchPi binds one key to each pair of the eight actions and presses it over a scoped list,
// a filtered single-model list and an empty filter result, so the first matching branch in Pi's order decides every probe.
func TestModelSelectorOverlappingBindingsMatchPi(t *testing.T) {
	actions := []string{"tui.input.tab", "tui.select.up", "tui.select.down", "tui.select.pageUp", "tui.select.pageDown", "tui.select.confirm", "tui.select.cancel", "app.models.save"}
	var all []modelOracleItem
	for _, spec := range []string{"anthropic/claude-a", "anthropic/claude-b", "openai/gpt-4o", "openai/gpt-4o-mini", "groq/llama-3", "google/gemini-x", "openrouter/llama-3", "xai/grok", "mistral/large", "mistral/small", "deepseek/chat", "cohere/command"} {
		provider, id, _ := strings.Cut(spec, "/")
		all = append(all, modelOracleItem{Provider: provider, ID: id, Name: strings.ToUpper(id[:1]) + id[1:] + " Model"})
	}
	scoped := []string{"openai/gpt-4o", "anthropic/claude-b", "groq/llama-3"}
	scripts := [][]string{
		{"\x19"},
		{"\x1b[B", "\x19"},
		{"\x1b[B", "\x1b[B", "\x19", "\x19"},
		{"g", "r", "o", "k", "\x19", "\x1b[B"},
		{"z", "z", "z", "\x19", "\x19"},
	}
	var probes []modelOracleProbe
	for i, first := range actions {
		for _, second := range actions[i+1:] {
			for _, saveCallback := range []bool{true, false} {
				for _, scopedModels := range [][]string{scoped, nil} {
					for _, keys := range scripts {
						probes = append(probes, modelOracleProbe{Theme: "dark", Width: 100, All: all, Scoped: scopedModels, Current: "anthropic/claude-b", SaveCallback: saveCallback, Bindings: map[string][]string{first: {"ctrl+y"}, second: {"ctrl+y"}}, Keys: keys})
					}
				}
			}
		}
	}
	// A page key on an empty, a single-model and a twelve-model list, and each action alone on an empty list.
	for _, action := range actions {
		for _, keys := range [][]string{{"z", "z", "z", "\x19"}, {"g", "r", "o", "k", "\x19"}, {"\x19", "\x19", "\x19"}} {
			probes = append(probes, modelOracleProbe{Theme: "dark", Width: 100, All: all, Scoped: scoped, SaveCallback: true, Bindings: map[string][]string{action: {"ctrl+y"}}, Keys: keys})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model_selector.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []modelOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d probes, sent %d", len(expected), len(probes))
	}
	previousBindings, previousTheme, previousCaps := GetKeybindings(), ActiveTheme(), GetCapabilities()
	t.Cleanup(func() {
		SetKeybindings(previousBindings)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	toItems := func(specs []string) []ModelSelectorItem {
		var out []ModelSelectorItem
		for _, spec := range specs {
			provider, id, _ := strings.Cut(spec, "/")
			out = append(out, ModelSelectorItem{Provider: provider, ID: id, Name: strings.ToUpper(id[:1]) + id[1:] + " Model"})
		}
		return out
	}
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		restoreKeybindingsAfterTest(t)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		selector := NewStaticModelSelectorComponent("Select model", toItems(probe.Scoped), toItems(probeSpecs(probe.All)), probe.Current)
		if !probe.SaveCallback {
			selector.noSaveDefault = true
			selector.Remove(selector.saveHint)
		}
		selector.SetStatus("Refreshing model catalogs…")
		got := modelOracleResult{Steps: [][]string{{}}, Frames: [][]string{selector.Render(probe.Width)}}
		var events []string
		completedAt := len(probe.Keys)
		for keyIndex, key := range probe.Keys {
			selector.HandleInput(key)
			if selector.Done() {
				switch {
				case selector.Cancelled():
					events = append(events, "cancel")
				case selector.SelectedAsDefault():
					events = append(events, "default:"+selector.SelectedFQ())
				default:
					events = append(events, "select:"+selector.SelectedFQ())
				}
			}
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
			if selector.Done() {
				completedAt = keyIndex + 1
				break
			}
		}
		want := expected[i]
		want.Steps, want.Frames = want.Steps[:completedAt+1], want.Frames[:completedAt+1]
		if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Frames, want.Frames) {
			failures++
			if failures <= 6 {
				t.Errorf("probe %d keys=%q bindings=%v saveCallback=%v scoped=%v:\nsteps = %q\nPi    = %q\n%s", i, probe.Keys, probe.Bindings, probe.SaveCallback, probe.Scoped, got.Steps, want.Steps, modelFrameDifference(got.Frames, want.Frames))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
