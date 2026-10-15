package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type oauthProbe struct {
	Mode      string               `json:"mode"`
	Providers []oauthProbeProvider `json:"providers"`
	Search    string               `json:"search,omitempty"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
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

// randomOAuthProbes draws provider lists of 0 to 30 entries (repeated ids, mixed case, wide and combining names), an initial search, and key
// scripts of typed text, cursor movement, word deletion, paste and selection keys.
func randomOAuthProbes() []oauthProbe {
	rng := rand.New(rand.NewSource(20261026))
	names := []string{"Anthropic", "OpenAI", "GitHub Copilot", "google vertex", "Mistral AI", "日本語 Provider", "e\u0301cole", "xAI", "Azure OpenAI", "openrouter", "A very long provider display name that needs truncating at narrow widths", "ollama-local", "😀 fun", "Z.AI", "Amazon Bedrock"}
	// Pi 1.1.0 has no select page keys; Pig pages the selection (D155, Pi PR #7680), so PageUp and PageDown stay out of the script.
	keys := []string{"\x1b[A", "\x1b[B", "\r", "\n", "\x1b", "\x03", "\x7f", "\x1b[3~", "\x1b[D", "\x1b[C", "\x01", "\x05", "\x17", "\x15", "\x0b", "a", "o", "p", "e", "n", " ", "-", "é", "日", "\x1b[200~gith\x1b[201~", "\x1b[1;5D", "\x1b[1;5C"}
	var probes []oauthProbe
	for range 400 {
		var list []oauthProbeProvider
		for range rng.Intn(31) {
			name := names[rng.Intn(len(names))]
			list = append(list, oauthProbeProvider{ID: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Name: name, AuthType: []string{"oauth", "api_key"}[rng.Intn(2)]})
		}
		if list == nil {
			list = []oauthProbeProvider{}
		}
		script := make([]string, 1+rng.Intn(16))
		for i := range script {
			script[i] = keys[rng.Intn(len(keys))]
		}
		probe := oauthProbe{Mode: []string{"login", "logout"}[rng.Intn(2)], Providers: list, Keys: script}
		if rng.Intn(4) == 0 {
			probe.Search = names[rng.Intn(len(names))][:1+rng.Intn(3)]
		}
		probes = append(probes, probe)
	}
	return probes
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
	probes = append(probes, randomOAuthProbes()...)
	// A cursor in the middle of a list longer than the eight visible rows: the window starts four rows above it (oauth-selector.ts startIndex).
	{
		var list []oauthProbeProvider
		for i := range 20 {
			list = append(list, oauthProbeProvider{ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("Provider %02d", i), AuthType: "oauth"})
		}
		down := "\x1b[B"
		for n := 5; n <= 9; n++ {
			probes = append(probes, oauthProbe{Mode: "login", Providers: list, Keys: slices.Repeat([]string{down}, n)})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/oauth_selector.mjs", pigversion.UpstreamPackageVersion)
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
