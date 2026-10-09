package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-status.test.ts:1220,1235
func TestLoadedResourcesQuietReloadUpstream(t *testing.T) {
	t.Run("does not show verbose listing on quiet startup during reload", func(t *testing.T) {
		m := upstreamListingMode(t, false, []upstreamExtensionFixture{{path: "/tmp/ext/index.ts"}})
		m.opts.Settings.QuietStartup = QuietStartupTrue
		m.opts.Skills = []*SkillDef{{FilePath: "/tmp/skill/SKILL.md", Name: "commit"}}
		m.showLoadedResources(false, true)
		if m.loadedResourcesContainer.ChildCount() != 0 {
			t.Fatal("quiet reload showed resources")
		}
	})
	t.Run("still shows diagnostics on quiet startup when requested", func(t *testing.T) {
		m := upstreamListingMode(t, false, nil)
		m.opts.Settings.QuietStartup = QuietStartupTrue
		m.opts.Skills = []*SkillDef{{FilePath: "/tmp/skill/SKILL.md", Name: "commit"}}
		m.opts.SkillDiagnostics = []extension.ResourceDiagnostic{{Type: extension.DiagnosticWarning, Message: "duplicate skill name"}}
		m.showLoadedResources(false, true)
		text := renderListing(m)
		if !strings.Contains(text, "[Skill conflicts]") || strings.Contains(text, "[Skills]") {
			t.Fatalf("quiet diagnostics=%q", text)
		}
	})
}
