package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type modelOracleItem struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Name     string `json:"name"`
}

type modelOracleProbe struct {
	Theme         string            `json:"theme"`
	Width         int               `json:"width"`
	All           []modelOracleItem `json:"all"`
	Scoped        []string          `json:"scoped"`
	Current       string            `json:"current,omitempty"`
	DefaultModel  string            `json:"defaultModel,omitempty"`
	InitialSearch string            `json:"initialSearch,omitempty"`
	SaveCallback  bool              `json:"saveCallback"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type modelOracleResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

// model-selector.ts handleInput against pinned Pi: scope toggle (only with scoped models), wrapped up/down, confirm, cancel, the
// save binding (only with an onSelectAsDefault callback) and every other key editing the search Input, whose own submit selects. Every frame
// is compared byte for byte (colours, search cursor and unpadded rows included), also at widths 20, 36 and 64.
func TestModelSelectorInputMatchesPi(t *testing.T) {
	var all []modelOracleItem
	for _, spec := range []string{"anthropic/claude-a", "anthropic/claude-b", "openai/gpt-4o", "openai/gpt-4o-mini", "groq/llama-3", "google/gemini-x", "openrouter/llama-3", "xai/grok", "mistral/large", "mistral/small", "deepseek/chat", "cohere/command"} {
		provider, id, _ := strings.Cut(spec, "/")
		all = append(all, modelOracleItem{Provider: provider, ID: id, Name: strings.ToUpper(id[:1]) + id[1:] + " Model"})
	}
	scripts := [][]string{
		{},
		{"\x1b[B", "\x1b[B", "\x1b[A", "\r"},
		{"\x1b[A", "\x1b[A", "\r"},
		{"\x1b[A", "\x1b[B"},
		{"\t", "\x1b[B", "\t", "\x1b[B"},
		{"\x1b"},
		{"\x03"},
		{"\x13"},
		{"\x1b[B", "\x13"},
		{"l", "l", "a", "m", "a", "\x1b[B", "\r"},
		{"z", "z", "z", "\x1b[A", "\r", "\x13", "\x1b[B"},
		{"g", "p", "t", "\x7f", "\x7f", "\x1b[D", "x", "\x13"},
		// Pi 1.1.0 has no select page keys; Pig pages the selection (D155, Pi PR #7680), covered by the select page key tests, so the oracle input leaves PageUp and PageDown out.
		{"\x1b[B", "\r"},
		{"d", "e", "f", "\x1b[B", "\r"},
		// JavaScript trim() strips U+FEFF and keeps U+0085 and U+00A0 around a query that is a prefix of "default" (isDefaultSearch).
		{"\ufeff", "d", "e", "f", "\x1b[B", "\r"},
		{"d", "e", "\ufeff", "\x1b[B", "\r"},
		{"\u0085", "d", "\x1b[B", "\r"},
		{"\u00a0", "d", "e", "\x1b[B", "\r"},
		{"a", "\x01", "x", "\x05", "y", "\n"},
		{"\x18", "q", "\r"},
		{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.confirm": {"ctrl+y"}},
		{"app.models.save": {"ctrl+g"}},
		{"app.models.save": {"ctrl+y"}, "tui.select.confirm": {"ctrl+y"}},
		{"tui.select.up": {"ctrl+p"}, "tui.select.down": {"ctrl+n"}, "tui.input.tab": {"ctrl+t"}},
		{"tui.select.cancel": {"ctrl+x"}, "app.models.save": {"escape"}},
	}
	sets := []struct {
		scoped        []string
		current, def  string
		initialSearch string
	}{
		{[]string{"openai/gpt-4o", "anthropic/claude-b", "groq/llama-3"}, "anthropic/claude-b", "", ""},
		{nil, "openai/gpt-4o-mini", "groq/llama-3", ""},
		{[]string{"google/gemini-x"}, "", "xai/grok", "g"},
		{nil, "", "", ""},
	}
	var probes []modelOracleProbe
	for _, theme := range []string{"dark", "light"} {
		for _, set := range sets {
			for _, saveCallback := range []bool{true} {
				for _, b := range bindings {
					for _, keys := range scripts {
						probes = append(probes, modelOracleProbe{Theme: theme, Width: 100, All: all, Scoped: set.scoped, Current: set.current, DefaultModel: set.def, InitialSearch: set.initialSearch, SaveCallback: saveCallback, Bindings: b, Keys: keys})
					}
				}
			}
		}
	}
	// Narrow terminals clip the rows, the scope line and the status line.
	for _, width := range []int{20, 36, 64, 1, 2, 3, 4, 5, 6, 7, 9, 12} {
		for _, set := range sets {
			for _, keys := range scripts {
				probes = append(probes, modelOracleProbe{Theme: "dark", Width: width, All: all, Scoped: set.scoped, Current: set.current, DefaultModel: set.def, InitialSearch: set.initialSearch, SaveCallback: true, Keys: keys})
			}
		}
	}
	probes = append(probes, randomModelSelectorProbes(bindings)...)
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
		if probe.DefaultModel != "" {
			selector.SetDefaultModel(probe.DefaultModel)
		}
		selector.SetFilter(probe.InitialSearch)
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
				t.Errorf("probe %d keys=%q bindings=%v scoped=%v current=%q default=%q search=%q:\nsteps = %q\nPi    = %q\n%s", i, probe.Keys, probe.Bindings, probe.Scoped, probe.Current, probe.DefaultModel, probe.InitialSearch, got.Steps, want.Steps, modelFrameDifference(got.Frames, want.Frames))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// randomModelSelectorProbes draws catalogues of 0 to 40 models (repeated provider and id parts, wide and combining text, long identifiers), scoped subsets, current and default models, an initial search, key scripts that type, edit and move through the list, the scope toggle and the save binding, and widths from 12 to 120.
func randomModelSelectorProbes(bindings []map[string][]string) []modelOracleProbe {
	rng := rand.New(rand.NewSource(20261021))
	providers := []string{"anthropic", "openai", "groq", "google", "openrouter", "xai", "mistral", "日本プロバイダ", "p", "a-very-long-provider-name-here"}
	ids := []string{"claude-a", "gpt-4o", "gpt-4o-mini", "llama-3", "gemini-x", "grok", "large", "small", "chat", "command", "m", "model-日本語", "e\u0301clair", "id-with-a-really-long-identifier-0123456789-abcdefghij", "x1", "x2"}
	keys := []string{"\x1b[A", "\x1b[B", "\r", "\n", "\t", "\x1b", "\x03", "\x13", "\x7f", "\x1b[D", "\x1b[C", "\x01", "\x05", "\x15", "\x0b", "g", "p", "t", "l", "a", "m", " ", "日", "/", "-", "e\u0301"}
	var probes []modelOracleProbe
	for range 400 {
		all := []modelOracleItem{}
		seen := map[string]bool{}
		for range rng.Intn(40) {
			provider, id := providers[rng.Intn(len(providers))], ids[rng.Intn(len(ids))]
			if seen[provider+"/"+id] {
				continue
			}
			seen[provider+"/"+id] = true
			all = append(all, modelOracleItem{Provider: provider, ID: id, Name: strings.ToUpper(id[:1]) + id[1:] + " Model"})
		}
		specs := func(n int) []string {
			var out []string
			for range n {
				if len(all) > 0 {
					m := all[rng.Intn(len(all))]
					out = append(out, m.Provider+"/"+m.ID)
				}
			}
			return out
		}
		var scoped []string
		if rng.Intn(2) == 0 {
			scoped = specs(1 + rng.Intn(6))
		}
		var current, def string
		if rng.Intn(2) == 0 {
			current = strings.Join(specs(1), "")
		}
		if rng.Intn(3) == 0 {
			def = strings.Join(specs(1), "")
		}
		search := ""
		if rng.Intn(4) == 0 {
			search = []string{"g", "gpt", "claude", "日", "l", "x"}[rng.Intn(6)]
		}
		script := make([]string, rng.Intn(16))
		for i := range script {
			script[i] = keys[rng.Intn(len(keys))]
		}
		probes = append(probes, modelOracleProbe{Theme: []string{"dark", "light"}[rng.Intn(2)], Width: 12 + rng.Intn(109), All: all, Scoped: scoped, Current: current, DefaultModel: def, InitialSearch: search, SaveCallback: true, Bindings: bindings[rng.Intn(len(bindings))], Keys: script})
	}
	return probes
}

// probeSpecs names each model of a probe's catalogue as provider/id.
func probeSpecs(items []modelOracleItem) []string {
	specs := make([]string, len(items))
	for i, m := range items {
		specs[i] = m.Provider + "/" + m.ID
	}
	return specs
}

func modelFrameDifference(got, want [][]string) string {
	for f := range min(len(got), len(want)) {
		for l := range max(len(got[f]), len(want[f])) {
			var g, w string
			if l < len(got[f]) {
				g = got[f][l]
			}
			if l < len(want[f]) {
				w = want[f][l]
			}
			if g != w {
				return fmt.Sprintf("frame %d line %d:\n  pig %q\n  Pi  %q", f, l, g, w)
			}
		}
	}
	return "frames equal"
}
