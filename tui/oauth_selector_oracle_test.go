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

type oauthProbe struct {
	Mode      string               `json:"mode"`
	Providers []oauthProbeProvider `json:"providers"`
	Search    string               `json:"search,omitempty"`
	Bindings  map[string][]string  `json:"bindings,omitempty"`
	Keys      []string             `json:"keys"`
}

type oauthProbeProvider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	AuthType string `json:"authType"`
}

type oauthProbeState struct {
	Events []string `json:"events"`
	Lines  []string `json:"lines"`
}

// Pi oauth-selector.ts handleInput: up and down clamp inside the filtered list, confirm calls onSelect(id, authType)
// for the highlighted provider and does nothing on an empty list, cancel calls onCancel, and every other key edits
// the search input and refilters. Each probe key is followed by the rendered component.
func TestOAuthSelectorHandleInputMatchesPi(t *testing.T) {
	providers := []oauthProbeProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "anthropic", Name: "Anthropic", AuthType: "api_key"},
		{ID: "openai", Name: "OpenAI", AuthType: "api_key"},
		{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth"},
	}
	probes := []oauthProbe{
		{Mode: "login", Providers: providers, Keys: []string{"\r"}},
		{Mode: "login", Providers: providers, Keys: []string{"\x1b[A", "\x1b[A", "\r"}},
		{Mode: "login", Providers: providers, Keys: []string{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\r"}},
		{Mode: "login", Providers: providers, Keys: []string{"\x1b[B", "\x1b[A", "\x1b[B", "\x1b[B", "\n"}},
		{Mode: "logout", Providers: providers, Keys: []string{"\x1b"}},
		{Mode: "login", Providers: providers, Keys: []string{"\x03"}},
		{Mode: "login", Providers: providers, Keys: []string{"o", "p", "\r"}},
		{Mode: "login", Providers: providers, Keys: []string{"c", "o", "p", "i", "\x1b[B", "\r"}},
		{Mode: "login", Providers: providers, Keys: []string{"z", "z", "\r", "\x7f", "\x7f", "\r"}},
		{Mode: "login", Providers: providers, Search: "anth", Keys: []string{"\x1b[B", "\r"}},
		{Mode: "login", Providers: providers, Keys: []string{"a", "\x1b[B", "\x1b[B", "x", "\x7f", "\r"}},
		{Mode: "login", Providers: providers[:1], Keys: []string{"\x1b[B", "\x1b[A", "\r"}},
		{Mode: "login", Providers: []oauthProbeProvider{}, Keys: []string{"\x1b[B", "\r", "\x1b"}},
		{Mode: "login", Providers: providers, Bindings: map[string][]string{KBSelectConfirm: {"ctrl+y"}, KBSelectCancel: {"ctrl+q"}}, Keys: []string{"\x19"}},
		{Mode: "login", Providers: providers, Bindings: map[string][]string{KBSelectConfirm: {"ctrl+y"}, KBSelectCancel: {"ctrl+q"}}, Keys: []string{"\x11"}},
		{Mode: "login", Providers: providers, Bindings: map[string][]string{KBSelectUp: {"ctrl+p"}, KBSelectDown: {"ctrl+n"}}, Keys: []string{"\x0e", "\x0e", "\x10", "\r"}},
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/oauth_selector.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		States []oauthProbeState `json:"states"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previous := GetKeybindings()
	t.Cleanup(func() { SetKeybindings(previous) })
	for i, probe := range probes {
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		var events []string
		var list []OAuthProvider
		for _, p := range probe.Providers {
			list = append(list, OAuthProvider{ID: p.ID, Name: p.Name, AuthType: p.AuthType})
		}
		var component *OAuthSelectorComponent
		if probe.Search != "" {
			component = NewOAuthSelectorComponent(probe.Mode, list, func(id, authType string) { events = append(events, "select "+id+"/"+authType) }, func() { events = append(events, "cancel") }, probe.Search)
		} else {
			component = NewOAuthSelectorComponent(probe.Mode, list, func(id, authType string) { events = append(events, "select "+id+"/"+authType) }, func() { events = append(events, "cancel") })
		}
		got := make([]oauthProbeState, 0, len(probe.Keys))
		for _, key := range probe.Keys {
			component.HandleInput(key)
			lines := component.Render(80)
			for j := range lines {
				lines[j] = strings.TrimRight(stripANSI(lines[j]), " ")
			}
			got = append(got, oauthProbeState{Events: append([]string{}, events...), Lines: lines})
			if len(events) > 0 {
				break
			}
		}
		if !reflect.DeepEqual(got, expected[i].States) {
			t.Errorf("probe %d %+v:\n  Pig %+v\n  Pi  %+v", i, probe, got, expected[i].States)
		}
	}
}
