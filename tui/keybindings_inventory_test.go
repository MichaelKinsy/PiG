package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/parity/knowngaps"
)

type upstreamKeybindingInventory struct {
	Keybindings []struct {
		ID          string              `json:"id"`
		Defaults    map[string][]string `json:"defaults"`
		Description string              `json:"description"`
	} `json:"keybindings"`
}

// currentBehaviorInputs reads the behavior-input inventory for the pinned Pi
// version and returns the repository root.
func currentBehaviorInputs(t *testing.T) (string, []byte) {
	t.Helper()
	root, err := filepath.Abs("..")
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
	return root, data
}

// TestTUIKeybindingDefinitionsMatchUpstreamInventory compares every tui.*
// binding on each platform column with the pinned Pi inventory. A missing, differing, or extra
// binding is a gap keyed "keybinding:<id>"; parity/known-gaps.toml lists the
// tolerated ones, and a listed gap that closes fails the test.
func TestTUIKeybindingDefinitionsMatchUpstreamInventory(t *testing.T) {
	root, data := currentBehaviorInputs(t)
	var inventory upstreamKeybindingInventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	gaps, err := knowngaps.NewTracker(root, "tui-keybindings")
	if err != nil {
		t.Fatal(err)
	}
	report := func(id, format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		if entry, ok := gaps.Gap("keybinding:" + id); ok {
			t.Logf("known gap [keybinding:%s] (%s): %s", id, entry.Tracking, message)
			return
		}
		t.Errorf("[keybinding:%s] %s", id, message)
	}
	reported := map[string]bool{}
	for _, platform := range KeybindingPlatforms {
		definitions := TUIKeybindingDefinitionsFor(platform)
		upstream := map[string]bool{}
		for _, binding := range inventory.Keybindings {
			if !strings.HasPrefix(binding.ID, "tui.") {
				continue
			}
			upstream[binding.ID] = true
			expected := TUIKeybindingDef{DefaultKeys: binding.Defaults[string(platform)], Description: binding.Description}
			got, exists := definitions[binding.ID]
			switch {
			case reported[binding.ID]:
			case !exists:
				reported[binding.ID] = true
				report(binding.ID, "missing TUI keybinding")
			case !slices.Equal(got.DefaultKeys, expected.DefaultKeys) || got.Description != expected.Description:
				reported[binding.ID] = true
				report(binding.ID, "%s = %#v, want %#v", platform, got, expected)
			}
		}
		for id := range definitions {
			if !upstream[id] && !reported[id] {
				reported[id] = true
				report(id, "PiG defines a TUI keybinding the pinned Pi does not")
			}
		}
	}
	if stale := gaps.Stale(); len(stale) > 0 {
		t.Errorf("parity/known-gaps.toml lists keybinding gaps that are now closed; remove them: %v", stale)
	}
}
