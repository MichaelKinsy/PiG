package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/parity/knowngaps"
	"github.com/MichaelKinsy/PiG/tui"
)

type upstreamAppKeybindingInventory struct {
	Keybindings []struct {
		ID          string              `json:"id"`
		Defaults    map[string][]string `json:"defaults"`
		Description string              `json:"description"`
	} `json:"keybindings"`
}

// TestAppKeybindingDefinitionsMatchUpstreamInventory compares every app.*
// binding on each platform with the pinned Pi inventory. A missing, differing,
// or extra binding is a gap keyed "keybinding:<id>"; parity/known-gaps.toml
// lists the tolerated ones, and a listed gap that closes fails the test.
func TestAppKeybindingDefinitionsMatchUpstreamInventory(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	version, err := knowngaps.PinnedVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "parity", "interfaces", "behavior-inputs-v"+version+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory upstreamAppKeybindingInventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	gaps, err := knowngaps.NewTracker(root, "app-keybindings")
	if err != nil {
		t.Fatal(err)
	}
	reported := map[string]bool{}
	report := func(id, format string, args ...any) {
		if reported[id] {
			return
		}
		reported[id] = true
		message := fmt.Sprintf(format, args...)
		if entry, ok := gaps.Gap("keybinding:" + id); ok {
			t.Logf("known gap [keybinding:%s] (%s): %s", id, entry.Tracking, message)
			return
		}
		t.Errorf("[keybinding:%s] %s", id, message)
	}
	for _, platform := range tui.KeybindingPlatforms {
		gotDefinitions := appKeybindingDefinitionsFor(platform)
		upstream := map[string]bool{}
		for _, binding := range inventory.Keybindings {
			if !strings.HasPrefix(binding.ID, "app.") {
				continue
			}
			upstream[binding.ID] = true
			description := binding.Description
			if binding.ID == "app.tools.expand" {
				// pig divergence (D59): Ctrl+O reveals generic arguments as well as output.
				description = "Toggle tool details"
			}
			expected := KeybindingDefinition{DefaultKeys: binding.Defaults[string(platform)], Description: description}
			got, exists := gotDefinitions[binding.ID]
			switch {
			case !exists:
				report(binding.ID, "missing app keybinding")
			case !slices.Equal(got.DefaultKeys, expected.DefaultKeys) || got.Description != expected.Description:
				report(binding.ID, "%s = %#v, want %#v", platform, got, expected)
			}
		}
		for id := range gotDefinitions {
			if !upstream[id] {
				report(id, "PiG defines an app keybinding the pinned Pi does not")
			}
		}
	}
	if stale := gaps.Stale(); len(stale) > 0 {
		t.Errorf("parity/known-gaps.toml lists keybinding gaps that are now closed; remove them: %v", stale)
	}
}

// Every default app key must reach an action through Matches: either the
// static sequence table or the generic alt+<printable> decoder. A default the
// matcher cannot see is keyboard-dead, as Windows ctrl+q follow-up would be.
func TestAppKeybindingDefaultsAreMatchable(t *testing.T) {
	for _, platform := range tui.KeybindingPlatforms {
		for id, definition := range appKeybindingDefinitionsFor(platform) {
			for _, key := range definition.DefaultKeys {
				if _, ok := keyIDInputs[key]; !ok && !isGenericPrintableKeyID(string(key)) {
					t.Errorf("%s %s default %q has no input sequence", platform, id, key)
				}
			}
		}
	}
}
