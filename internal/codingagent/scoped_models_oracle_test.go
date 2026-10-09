package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type scopedOracleModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

type scopedOracleProbe struct {
	Theme         string              `json:"theme"`
	Width         int                 `json:"width"`
	Models        []scopedOracleModel `json:"models"`
	Enabled       []string            `json:"enabled"`
	RefreshStatus string              `json:"refreshStatus"`
	Bindings      map[string][]string `json:"bindings,omitempty"`
	Keys          []string            `json:"keys"`
}

type scopedOracleStep struct {
	Enabled   []string `json:"enabled"`
	Saved     []string `json:"saved"`
	DidSave   bool     `json:"didSave"`
	Cancelled bool     `json:"cancelled"`
}

type scopedOracleResult struct {
	Steps  []scopedOracleStep `json:"steps"`
	Frames [][]string         `json:"frames"`
	Crash  *string            `json:"crash"`
}

// scoped-models-selector.ts handleInput against pinned Pi: wrap navigation, fuzzy search, enable/clear all (filtered when a query
// is active), provider toggle, bounded reorder, unavailable IDs, save, and Ctrl+C/Escape; the enabled list after each key (Pi's
// onChange payload), the save payload, cancellation and every frame's text must agree.
func TestScopedModelsSelectorMatchesPi(t *testing.T) {
	models := []scopedOracleModel{
		{"claude-sonnet", "Claude Sonnet", "anthropic"}, {"claude-haiku", "Claude Haiku", "anthropic"},
		{"gpt-5", "GPT 5", "openai"}, {"gpt-5-mini", "GPT 5 Mini", "openai"}, {"o4", "O4", "openai"},
		{"gemini-pro", "Gemini Pro", "google"}, {"gemini-flash", "Gemini Flash", "google"},
		{"llama-3", "Llama 3", "local"}, {"qwen", "Qwen", "local"}, {"mistral-large", "Mistral Large", "mistral"},
		{"grok-4", "Grok 4", "xai"}, {"deepseek", "DeepSeek", "deepseek"},
	}
	enabledSets := [][]string{
		nil,
		{},
		{"openai/gpt-5", "anthropic/claude-sonnet", "local/qwen"},
		{"google/gemini-pro", "ghost/removed-model", "openai/o4", "anthropic/claude-haiku"},
		{"anthropic/claude-sonnet", "anthropic/claude-haiku", "openai/gpt-5", "openai/gpt-5-mini", "openai/o4", "google/gemini-pro", "google/gemini-flash", "local/llama-3", "local/qwen", "mistral/mistral-large", "xai/grok-4", "deepseek/deepseek"},
	}
	bindings := []map[string][]string{
		nil,
		{"app.models.save": {"ctrl+y"}, "app.models.clearAll": {"ctrl+k"}, "app.models.enableAll": {"ctrl+e"}},
		{"app.models.reorderUp": {"ctrl+u"}, "app.models.reorderDown": {"ctrl+d"}, "app.models.toggleProvider": {"ctrl+t"}},
		{"tui.select.confirm": {"space"}, "app.models.save": {"enter"}},
		{"tui.select.up": {"ctrl+p"}, "app.models.toggleProvider": {"ctrl+p"}},
		{"app.models.save": {"up"}, "app.models.enableAll": {"escape"}},
	}
	alphabet := []string{"\x1b[A", "\x1b[A", "\x1b[B", "\x1b[B", "\x1b[B", "\r", "\r", "\r", " ", "\x01", "\x18", "\x10", "\x1b[1;3A", "\x1b[1;3B", "\x13", "\x1b", "\x03", "\x7f", "g", "p", "t", "c", "l", "a", "o", "-", "5", "\x19", "\x0b", "\x05", "\x15", "\x14", "\x04", "\n"}
	rng := rand.New(rand.NewSource(20261009))
	var probes []scopedOracleProbe
	for _, theme := range []string{"dark", "light"} {
		for _, enabled := range enabledSets {
			for _, b := range bindings {
				for range 8 {
					keys := make([]string, 6+rng.Intn(40))
					for i := range keys {
						keys[i] = alphabet[rng.Intn(len(alphabet))]
					}
					status := ""
					if rng.Intn(3) == 0 {
						status = "Refreshing model catalogs…"
					}
					probes = append(probes, scopedOracleProbe{Theme: theme, Width: 100, Models: models, Enabled: enabled, RefreshStatus: status, Bindings: b, Keys: keys})
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/scoped_models.mjs", pigversion.UpstreamVersion)
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
	previousTheme, previousCaps, previousKitty := tui.ActiveTheme(), tui.GetCapabilities(), tui.IsKittyProtocolActive()
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetKittyProtocolActive(previousKitty)
	})
	reachedSave, reachedCancel, reachedChange, crashes := 0, 0, 0, 0
	failures := 0
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
		list := tui.NewScopedModelsList(tui.ScopedModelsConfig{AllModels: items, EnabledModelIDs: probe.Enabled, RefreshStatus: probe.RefreshStatus})
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
		// Pi keeps feeding keys after a cancel; Pig's host stops at Done. A crash only counts when it happens before Pig is done.
		piKeys := len(want.Steps) - 1
		pigKeys := len(got.Steps) - 1
		switch {
		case want.Crash != nil && piKeys < pigKeys:
			failures++
			t.Errorf("probe %d: Pi crashed at key %d, Pig ran on to key %d (keys %q)", i, piKeys+1, pigKeys, probe.Keys)
			continue
		case want.Crash != nil && piKeys == pigKeys && !pigPanicked && !list.Done():
			failures++
			t.Errorf("probe %d: Pi crashed at key %d, Pig did not panic there (keys %q)", i, piKeys+1, probe.Keys)
			continue
		case pigPanicked && (want.Crash == nil || piKeys != pigKeys):
			failures++
			t.Errorf("probe %d: Pig panicked at key %d, Pi did not (keys %q)", i, pigKeys+1, probe.Keys)
			continue
		}
		if pigPanicked {
			crashes++ // both threw on the same key (a reorder moved the selection above the first filtered row)
		}
		want.Steps, want.Frames = want.Steps[:len(got.Steps)], want.Frames[:len(got.Frames)]
		for _, step := range want.Steps {
			if step.DidSave {
				reachedSave++
			}
		}
		if want.Steps[len(want.Steps)-1].Cancelled {
			reachedCancel++
		}
		if !reflect.DeepEqual(want.Steps[0].Enabled, want.Steps[len(want.Steps)-1].Enabled) {
			reachedChange++
		}
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
	if reachedSave < 20 || reachedCancel < 20 || reachedChange < 100 {
		t.Errorf("probes are too shallow: saves %d, cancels %d, changed selections %d", reachedSave, reachedCancel, reachedChange)
	}
	t.Logf("Pi crashed on %d of %d probes (reorder above the first filtered row); those compare up to the crash", crashes, len(probes))
	_ = fmt.Sprint
}

func safeStep(steps []scopedOracleStep, i int) any {
	if i < 0 || i >= len(steps) {
		return nil
	}
	return steps[i]
}
