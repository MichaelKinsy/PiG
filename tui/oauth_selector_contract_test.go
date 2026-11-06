package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Pi oauth-selector.ts handleInput tests, in order: up, down, page (getSelectPageIndex), confirm, cancel, then the search Input. Each neighbouring pair is
// bound to one key so the earlier branch must win in Pig as in Pi; first and last rows, one provider, no provider, the page window around the cursor, the
// search clamp (selectedIndex is clamped, not reset), and the search Input's own submit key run against the pinned Pi component with the rendered rows
// compared after every key.
func TestOAuthSelectorBranchOrderAndBoundariesMatchPi(t *testing.T) {
	var twenty, nine []oauthProbeProvider
	for i := range 20 {
		twenty = append(twenty, oauthProbeProvider{ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("Provider %02d", i), AuthType: "oauth"})
	}
	nine = twenty[:9]
	three := twenty[:3]
	down, up, pgDown, pgUp, enter, esc := "\x1b[B", "\x1b[A", "\x1b[6~", "\x1b[5~", "\r", "\x1b"
	ctrlX, ctrlY := "\x18", "\x19"
	bind := func(pairs ...string) map[string][]string {
		b := map[string][]string{}
		for i := 0; i < len(pairs); i += 2 {
			b[pairs[i]] = append(b[pairs[i]], pairs[i+1])
		}
		return b
	}
	probes := []oauthProbe{
		// page keys: eight visible rows, clamped at both ends, ignored on an empty list
		{Mode: "login", Providers: twenty, Keys: []string{pgDown, pgDown, pgDown, pgDown, enter}},
		{Mode: "login", Providers: twenty, Keys: []string{pgDown, pgDown, pgDown, pgUp, pgUp, pgUp, pgUp, enter}},
		{Mode: "login", Providers: twenty, Keys: []string{down, pgDown, pgUp, enter}},
		{Mode: "login", Providers: nine, Keys: []string{pgDown, pgDown, pgUp, pgUp, enter}},
		{Mode: "login", Providers: three, Keys: []string{pgDown, pgUp, pgDown, enter}},
		{Mode: "login", Providers: three[:1], Keys: []string{pgDown, pgUp, down, up, enter}},
		{Mode: "login", Providers: []oauthProbeProvider{}, Keys: []string{pgDown, pgUp, down, up, enter, esc}},
		{Mode: "logout", Providers: []oauthProbeProvider{}, Keys: []string{pgDown, enter}},
		// the first and last row do not wrap
		{Mode: "login", Providers: three, Keys: []string{up, up, down, down, down, down, enter}},
		{Mode: "login", Providers: twenty, Keys: slices.Concat(slices.Repeat([]string{down}, 21), []string{enter})},
		{Mode: "login", Providers: twenty, Keys: slices.Concat(slices.Repeat([]string{down}, 12), slices.Repeat([]string{up}, 14), []string{enter})},
		// up before down, down before page, page before confirm, confirm before cancel, cancel before the search Input
		{Mode: "login", Providers: three, Bindings: bind(KBSelectUp, "ctrl+x", KBSelectDown, "ctrl+x"), Keys: []string{down, down, ctrlX, ctrlX, enter}},
		{Mode: "login", Providers: twenty, Bindings: bind(KBSelectDown, "pageDown"), Keys: []string{pgDown, pgDown, enter}},
		{Mode: "login", Providers: twenty, Bindings: bind(KBSelectUp, "pageDown"), Keys: []string{down, down, pgDown, enter}},
		{Mode: "login", Providers: twenty, Bindings: bind(KBSelectDown, "pageUp"), Keys: []string{pgUp, pgUp, enter}},
		{Mode: "login", Providers: twenty, Bindings: bind(KBSelectConfirm, "pageDown"), Keys: []string{pgDown, enter}},
		{Mode: "login", Providers: twenty, Bindings: bind(KBSelectCancel, "pageUp"), Keys: []string{down, pgUp, enter}},
		{Mode: "login", Providers: three, Bindings: bind(KBSelectUp, "ctrl+x", KBSelectConfirm, "ctrl+x"), Keys: []string{down, ctrlX, ctrlX}},
		{Mode: "login", Providers: three, Bindings: bind(KBSelectDown, "ctrl+x", KBSelectConfirm, "ctrl+x"), Keys: []string{ctrlX, ctrlX, ctrlX}},
		{Mode: "login", Providers: three, Bindings: bind(KBSelectUp, "ctrl+x", KBSelectCancel, "ctrl+x"), Keys: []string{ctrlX, down, ctrlX}},
		{Mode: "login", Providers: three, Bindings: bind(KBSelectConfirm, "ctrl+y", KBSelectCancel, "ctrl+y"), Keys: []string{down, ctrlY}},
		{Mode: "login", Providers: three, Bindings: bind(KBSelectCancel, "q"), Keys: []string{"q"}},
		{Mode: "login", Providers: three, Bindings: bind(KBSelectCancel, "q"), Keys: []string{"o", "p", "q"}},
		// the search Input's submit selects the highlighted provider, and does nothing on an empty list
		{Mode: "login", Providers: three, Bindings: map[string][]string{KBSelectConfirm: {}}, Keys: []string{down, enter}},
		{Mode: "login", Providers: three, Bindings: bind(KBInputSubmit, "ctrl+s"), Keys: []string{down, "\x13"}},
		{Mode: "login", Providers: three, Keys: []string{"z", "z", "\x13", "\x7f", "\x7f", down, "\n"}},
		{Mode: "login", Providers: three, Keys: []string{"z", "z", "\n", enter, esc}},
		// the search edits the list and clamps, not resets, the highlighted row
		{Mode: "login", Providers: twenty, Keys: slices.Concat(slices.Repeat([]string{down}, 10), []string{"1", enter})},
		{Mode: "login", Providers: twenty, Keys: slices.Concat(slices.Repeat([]string{down}, 10), []string{"1", "9", "x", "\x7f", "\x7f", "\x7f", enter})},
		{Mode: "login", Providers: twenty, Keys: slices.Concat(slices.Repeat([]string{down}, 14), []string{"1", "2", pgUp, enter})},
		{Mode: "login", Providers: twenty, Search: "Provider 1", Keys: []string{pgDown, enter}},
		{Mode: "login", Providers: twenty, Search: "nomatch", Keys: []string{pgDown, down, enter, "\x7f", enter}},
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
		onSelect := func(id, authType string) { events = append(events, "select "+id+"/"+authType) }
		onCancel := func() { events = append(events, "cancel") }
		var component *OAuthSelectorComponent
		if probe.Search != "" {
			component = NewOAuthSelectorComponent(probe.Mode, list, onSelect, onCancel, probe.Search)
		} else {
			component = NewOAuthSelectorComponent(probe.Mode, list, onSelect, onCancel)
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
