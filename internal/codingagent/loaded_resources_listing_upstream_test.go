package codingagent

import (
	"strings"
	"testing"
)

// Ports packages/coding-agent/test/interactive-mode-status.test.ts:702,718,735,753. Upstream stubs formatScopeGroups to the sentinel "resource-list"; PiG renders the real scope-grouped body, so the expanded cases assert its exact text instead of the sentinel.
func TestLoadedResourcesSkillAndExtensionListingsUpstream(t *testing.T) {
	skill := []*SkillDef{{Path: "/tmp/skill/SKILL.md", Name: "commit"}}
	t.Run("shows a compact resource listing by default", func(t *testing.T) {
		m := upstreamListingMode(t, false, nil)
		m.opts.Skills = skill
		got := renderedListing(m)
		if !strings.Contains(got, "[Skills]") || !strings.Contains(got, "commit") || strings.Contains(got, "/tmp/skill") {
			t.Fatalf("compact listing=%q", got)
		}
	})
	for _, tc := range []struct {
		name    string
		verbose bool
		quiet   bool
		expand  bool
	}{
		{"shows full resource listing when expanded", false, false, true},
		{"shows full resource listing on verbose startup even when tool output is collapsed", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := upstreamListingMode(t, tc.expand, nil)
			m.opts.Skills = skill
			m.opts.Verbose, m.opts.Settings.QuietStartup = tc.verbose, tc.quiet
			got := renderedListing(m)
			if !strings.Contains(got, "[Skills]") || strings.Contains(got, "commit") || !strings.Contains(got, "/tmp/skill/SKILL.md") {
				t.Fatalf("expanded listing=%q", got)
			}
		})
	}
	t.Run("abbreviates extensions in compact listing", func(t *testing.T) {
		m := upstreamListingMode(t, false, []upstreamExtensionFixture{
			{path: "/tmp/extensions/answer.ts"},
			{path: "/tmp/extensions/btw.ts"},
		})
		got := renderedListing(m)
		if !strings.Contains(got, "[Extensions]") || !strings.Contains(got, "answer.ts, btw.ts") || strings.Contains(got, "extensions/answer.ts") {
			t.Fatalf("compact extensions=%q", got)
		}
	})
}
