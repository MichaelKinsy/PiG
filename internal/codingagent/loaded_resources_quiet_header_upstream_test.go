package codingagent

import (
	"encoding/json"
	"testing"
)

// setUpstreamQuietStartup applies an upstream quietStartup value (true, false or "header") through the settings JSON decoder, so the case reads the value the way settings.json supplies it.
func setUpstreamQuietStartup(t *testing.T, m *InteractiveMode, quietStartup any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"quietStartup": quietStartup})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &m.opts.Settings); err != nil {
		t.Fatalf("decode quietStartup %v: %v", quietStartup, err)
	}
}

// .upstream/v1.0.0/packages/coding-agent/test/interactive-mode-status.test.ts:1245,1260
func TestLoadedResourcesQuietStartupHeaderUpstream(t *testing.T) {
	// interactive-mode-status.test.ts:1245.
	t.Run("hides resource listing but keeps the startup header with header-only quiet startup", func(t *testing.T) {
		m := upstreamListingMode(t, false, nil)
		setUpstreamQuietStartup(t, m, "header")
		m.opts.Skills = []*SkillDef{{FilePath: "/tmp/skill/SKILL.md", Name: "commit"}}
		m.showLoadedResources(false, false)
		if got := m.loadedResourcesContainer.ChildCount(); got != 0 {
			t.Fatalf("loadedResourcesContainer children = %d, want 0", got)
		}
		if !m.shouldShowStartupHeader() {
			t.Fatal("shouldShowStartupHeader() = false, want true")
		}
		if m.shouldShowStartupDetails() {
			t.Fatal("shouldShowStartupDetails() = true, want false")
		}
	})
	// interactive-mode-status.test.ts:1260.
	t.Run("hides the startup header with full quiet startup unless verbose", func(t *testing.T) {
		quiet := upstreamListingMode(t, false, nil)
		setUpstreamQuietStartup(t, quiet, true)
		if quiet.shouldShowStartupHeader() {
			t.Fatal("quietStartup true: shouldShowStartupHeader() = true, want false")
		}
		verbose := upstreamListingMode(t, false, nil)
		setUpstreamQuietStartup(t, verbose, "header")
		verbose.opts.Verbose = true
		if !verbose.shouldShowStartupDetails() {
			t.Fatal("quietStartup header + verbose: shouldShowStartupDetails() = false, want true")
		}
	})
}
