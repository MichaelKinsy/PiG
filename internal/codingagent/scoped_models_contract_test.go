package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// Boundary obligations of ScopedModelsList.HandleInput (packages/coding-agent/src/modes/interactive/components/scoped-models-selector.ts
// handleInput) compared with the pinned Pi: the action order up, down, selection page keys, reorder up and down, confirm, enable all,
// clear all, toggle provider, save, Ctrl+C and Escape when one key is bound to two actions, and the cancel keys over a bound action.

// TestScopedModelsOverlappingBindingsMatchPi binds one key to each pair of the eleven bound actions, and Escape and Ctrl+C to each
// action, over an unfiltered, a filtered, a single-match and an empty list with a partial and a null enabled set.
func TestScopedModelsOverlappingBindingsMatchPi(t *testing.T) {
	restoreTUIKeybindings(t)
	models := []scopedOracleModel{
		{"claude-sonnet", "Claude Sonnet", "anthropic"}, {"claude-haiku", "Claude Haiku", "anthropic"},
		{"gpt-5", "GPT 5", "openai"}, {"gpt-5-mini", "GPT 5 Mini", "openai"}, {"o4", "O4", "openai"},
		{"gemini-pro", "Gemini Pro", "google"}, {"gemini-flash", "Gemini Flash", "google"},
		{"llama-3", "Llama 3", "local"}, {"qwen", "Qwen", "local"}, {"mistral-large", "Mistral Large", "mistral"},
		{"grok-4", "Grok 4", "xai"}, {"deepseek", "DeepSeek", "deepseek"},
	}
	enabledSets := [][]string{nil, {"openai/gpt-5", "anthropic/claude-sonnet", "local/qwen", "ghost/removed"}}
	actions := []string{"tui.select.up", "tui.select.down", "tui.select.pageUp", "tui.select.pageDown", "app.models.reorderUp", "app.models.reorderDown", "tui.select.confirm", "app.models.enableAll", "app.models.clearAll", "app.models.toggleProvider", "app.models.save"}
	scriptsFor := func(key string) [][]string {
		return [][]string{
			{key},
			{"\x1b[B", key},
			{"\x1b[B", "\x1b[B", key, key},
			{"g", "p", key, key},
			{"g", "p", "t", "-", "5", "-", "m", "i", key, "\x1b[B", key},
			{"z", "z", "z", key, key},
		}
	}
	var probes []scopedOracleProbe
	add := func(bindings map[string][]string, key string) {
		for _, enabled := range enabledSets {
			for _, keys := range scriptsFor(key) {
				probes = append(probes, scopedOracleProbe{Theme: "dark", Width: 100, Models: models, Enabled: enabled, Bindings: bindings, Keys: keys})
			}
		}
	}
	for i, first := range actions {
		for _, second := range actions[i+1:] {
			add(map[string][]string{first: {"ctrl+y"}, second: {"ctrl+y"}}, "\x19")
		}
	}
	for _, action := range actions {
		add(map[string][]string{action: {"ctrl+y"}}, "\x19")
		add(map[string][]string{action: {"escape"}}, "\x1b")
		add(map[string][]string{action: {"ctrl+c"}}, "\x03")
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/scoped_models.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []scopedOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d probes, sent %d", len(expected), len(probes))
	}
	previousTheme, previousCaps, previousKitty := tui.ActiveTheme(), tui.GetCapabilities(), tui.IsKittyProtocolActive()
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetKittyProtocolActive(previousKitty)
	})
	failures, crashes := 0, 0
	for i, probe := range probes {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
		tui.SetTheme(probe.Theme)
		bindingMap := map[string][]KeyID{}
		for action, keys := range probe.Bindings {
			for _, key := range keys {
				bindingMap[action] = append(bindingMap[action], KeyID(key))
			}
		}
		useKeybindings(t, bindingMap)
		items := make([]tui.ModelItem, len(probe.Models))
		for j, m := range probe.Models {
			items[j] = tui.ModelItem{FullID: m.Provider + "/" + m.ID, Name: m.Name, Provider: m.Provider}
		}
		list := tui.NewScopedModelsList(tui.ScopedModelsConfig{AllModels: items, EnabledModelIDs: probe.Enabled})
		snapshot := func() scopedOracleStep {
			step := scopedOracleStep{Enabled: list.EnabledIDs()}
			if saved, ok := list.ConsumeSave(); ok {
				step.Saved, step.DidSave = saved, true
			}
			step.Cancelled = list.Done() && list.Result().Cancelled
			return step
		}
		got := scopedOracleResult{Steps: []scopedOracleStep{snapshot()}, Frames: [][]string{list.Render(probe.Width)}}
		pigPanicked := false
		for _, key := range probe.Keys {
			func() {
				defer func() {
					if recover() != nil {
						pigPanicked = true
					}
				}()
				list.HandleInput(key)
				got.Steps = append(got.Steps, snapshot())
				got.Frames = append(got.Frames, list.Render(probe.Width))
			}()
			if pigPanicked || list.Done() {
				break
			}
		}
		want := expected[i]
		// Pi keeps feeding keys after a cancel and Pig's owner loop stops at Done; a crash counts only when it comes before Pig is done.
		piKeys, pigKeys := len(want.Steps)-1, len(got.Steps)-1
		switch {
		case want.Crash != nil && piKeys < pigKeys:
			failures++
			t.Errorf("probe %d: Pi crashed at key %d, Pig ran on to key %d (keys %q bindings %v)", i, piKeys+1, pigKeys, probe.Keys, probe.Bindings)
			continue
		case want.Crash != nil && piKeys == pigKeys && !pigPanicked && !list.Done():
			failures++
			t.Errorf("probe %d: Pi crashed at key %d, Pig did not panic there (keys %q bindings %v)", i, piKeys+1, probe.Keys, probe.Bindings)
			continue
		case pigPanicked && (want.Crash == nil || piKeys != pigKeys):
			failures++
			t.Errorf("probe %d: Pig panicked at key %d, Pi did not (keys %q bindings %v)", i, pigKeys+1, probe.Keys, probe.Bindings)
			continue
		}
		if pigPanicked {
			crashes++
		}
		want.Steps, want.Frames = want.Steps[:len(got.Steps)], want.Frames[:len(got.Frames)]
		normalize := func(steps []scopedOracleStep) []scopedOracleStep {
			out := make([]scopedOracleStep, len(steps))
			for j, s := range steps {
				if s.Enabled != nil && len(s.Enabled) == 0 {
					s.Enabled = []string{}
				}
				out[j] = s
			}
			return out
		}
		if !reflect.DeepEqual(normalize(got.Steps), normalize(want.Steps)) || !reflect.DeepEqual(got.Frames, want.Frames) {
			failures++
			if failures <= 5 {
				stepAt := -1
				for j := range got.Steps {
					if !reflect.DeepEqual(normalize(got.Steps[j:j+1]), normalize(want.Steps[j:j+1])) {
						stepAt = j
						break
					}
				}
				t.Errorf("probe %d bindings=%v enabled=%v keys=%q\nfirst step difference %d: Pig %+v, Pi %+v\n%s", i, probe.Bindings, probe.Enabled, probe.Keys, stepAt, safeStep(got.Steps, stepAt), safeStep(want.Steps, stepAt), firstFrameDifference(got.Frames, want.Frames))
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if crashes > len(probes)/10 {
		t.Errorf("Pi crashed on %d of %d probes; the probes no longer compare the actions", crashes, len(probes))
	}
}

// Once the selector is cancelled the owner loop ends delivery; a key that still reaches the component changes nothing: the
// enabled list, the dirty state and the result keep their values.
func TestScopedModelsIgnoreKeysAfterTheyCancel(t *testing.T) {
	items := []tui.ModelItem{{FullID: "openai/gpt-5", Name: "GPT 5", Provider: "openai"}, {FullID: "local/qwen", Name: "Qwen", Provider: "local"}}
	list := tui.NewScopedModelsList(tui.ScopedModelsConfig{AllModels: items, EnabledModelIDs: []string{"openai/gpt-5"}})
	list.HandleInput("\x1b")
	if !list.Done() || !list.Result().Cancelled {
		t.Fatal("escape did not cancel")
	}
	for _, key := range []string{"\r", "\x13", "\x01", "\x1b[B", "x"} {
		list.HandleInput(key)
	}
	if got := list.EnabledIDs(); !reflect.DeepEqual(got, []string{"openai/gpt-5"}) {
		t.Fatalf("a key after cancel changed the enabled list to %v", got)
	}
	if _, saved := list.ConsumeSave(); saved {
		t.Fatal("a key after cancel saved")
	}
	if !list.Result().Cancelled {
		t.Fatal("a key after cancel changed the result")
	}
}
