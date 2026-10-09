package tui

import (
	"reflect"
	"testing"
)

// oauth-selector.ts handleInput against pinned Pi: arrows clamp (no wrap), confirm selects the highlighted row of the filtered
// list, cancel cancels, a literal LF reaches the search Input whose submit selects, and every other key edits the fuzzy search
// (clamping the selection to the filtered list). Remapped bindings take the same paths.
func TestOAuthSelectorInputMatchesPi(t *testing.T) {
	providers := []map[string]any{
		{"id": "anthropic", "name": "Anthropic", "authType": "oauth"},
		{"id": "openai", "name": "OpenAI", "authType": "api_key"},
		{"id": "github-copilot", "name": "GitHub Copilot", "authType": "oauth"},
		{"id": "groq", "name": "Groq", "authType": "api_key"},
	}
	keys := [][]string{
		{"\x1b[A"},
		{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\r"},
		{"\x1b[B", "\x1b[A", "\x1b[A", "\r"},
		{"g", "r", "\x1b[B", "\r"},
		{"\x1b[B", "\x1b[B", "g", "i", "t", "\r"},
		{"z", "z", "\r"},
		{"\n"},
		{"\x1b[B", "x", "\x7f", "\n"},
		{"\x1b[B", "\x1b[5~", "\x1b[H", "\r"},
		{"\x1b"},
		{"\x03"},
		{"\x13"},
		{"a", "\x1b[D", "o", "\x17", "\r"},
	}
	bindings := []map[string][]string{
		nil,
		{KBSelectConfirm: {"ctrl+s"}},
		{KBSelectCancel: {"ctrl+x"}},
		{KBSelectDown: {"ctrl+s"}, KBSelectConfirm: {"ctrl+s"}},
		{"tui.input.submit": {"ctrl+s"}},
	}
	var probes []dialogProbe
	for _, theme := range []string{"dark", "light"} {
		for _, b := range bindings {
			for _, k := range keys {
				probes = append(probes, dialogProbe{Kind: "oauth", Theme: theme, TrueColor: true, Width: 60, Bindings: b, Keys: k, Providers: providers})
			}
		}
	}
	for _, keys := range [][]string{{"x", "\r"}, {"\x1b[F", "p", "\x1b[D", "\x17", "\r"}, {"\x1b[B", "\r"}} {
		probes = append(probes, dialogProbe{Kind: "oauth", Theme: "dark", TrueColor: true, Width: 60, Keys: keys, Providers: providers, InitialSearch: "gro"})
	}
	expected := piDialogOracle(t, probes)
	previousBindings, previousTheme, previousCaps := GetKeybindings(), ActiveTheme(), GetCapabilities()
	t.Cleanup(func() {
		SetKeybindings(previousBindings)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	goProviders := []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "openai", Name: "OpenAI", AuthType: "api_key"},
		{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth"},
		{ID: "groq", Name: "Groq", AuthType: "api_key"},
	}
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: probe.TrueColor})
		SetTheme(probe.Theme)
		definitions := TUIKeybindingDefinitionsFor(HostKeybindingPlatform())
		restoreKeybindingsAfterTest(t)
		SetKeybindings(NewKeybindingsManager(definitions, probe.Bindings))
		var selectedValue string
		selector := NewOAuthSelectorComponent("login", goProviders, func(providerID, authType string) { selectedValue = providerID + "/" + authType }, func() {}, probe.InitialSearch)
		selector.search.Focused = true
		render := func() []string {
			rows := selector.Render(probe.Width)
			return rows[2 : len(rows)-2]
		}
		got := dialogProbeResult{Frames: [][]string{render()}}
		for _, key := range probe.Keys {
			selector.HandleInput(key)
			state := dialogProbeState{Done: selector.Done(), Cancelled: selector.Cancelled()}
			if state.Done && !state.Cancelled {
				state.Value = selectedValue
			}
			got.States = append(got.States, state)
			if state.Done {
				break
			}
			got.Frames = append(got.Frames, render())
		}
		if !reflect.DeepEqual(got.States, expected[i].States) {
			t.Errorf("probe %d keys %q bindings %v: states = %+v; Pi = %+v", i, probe.Keys, probe.Bindings, got.States, expected[i].States)
		}
		if !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			t.Errorf("probe %d keys %q bindings %v:\n%s", i, probe.Keys, probe.Bindings, oracleFrameDifference(got.Frames, expected[i].Frames))
		}
	}
}
