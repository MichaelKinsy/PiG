package codingagent

import (
	"strings"
	"testing"
)

// pi: packages/coding-agent/src/utils/changelog.ts
//
// Cases of packages/coding-agent/test/changelog.test.ts (normalizeChangelogLinks): package-relative links become tag-pinned GitHub source links, and old
// repository URLs are canonicalized without touching external links or local anchors.
func TestPiCodingAgentSrcUtilsChangelogNormalizeLinks(t *testing.T) {
	t.Run("rewrites package-relative changelog links to tag-pinned GitHub source links", func(t *testing.T) {
		markdown := strings.Join([]string{
			"[Project Trust](README.md#project-trust)",
			"[Extensions](docs/extensions.md#project_trust)",
			"[Examples](examples/extensions/)",
			"[Root README](../../README.md#supply-chain-hardening)",
		}, "\n")
		want := strings.Join([]string{
			"[Project Trust](https://github.com/earendil-works/pi/blob/v0.79.0/packages/coding-agent/README.md#project-trust)",
			"[Extensions](https://github.com/earendil-works/pi/blob/v0.79.0/packages/coding-agent/docs/extensions.md#project_trust)",
			"[Examples](https://github.com/earendil-works/pi/tree/v0.79.0/packages/coding-agent/examples/extensions/)",
			"[Root README](https://github.com/earendil-works/pi/blob/v0.79.0/README.md#supply-chain-hardening)",
		}, "\n")
		if got := NormalizeChangelogLinks(markdown, ChangelogEntry{Major: 0, Minor: 79, Patch: 0}.Version()); got != want {
			t.Fatalf("NormalizeChangelogLinks = %q, want %q", got, want)
		}
	})
	t.Run("canonicalizes old repository URLs without changing external links", func(t *testing.T) {
		markdown := strings.Join([]string{
			"[#5167](https://github.com/earendil-works/pi-mono/pull/5167)",
			"[#4163](https://github.com/badlogic/pi-mono/issues/4163)",
			"[Agent README](https://github.com/badlogic/pi-mono/blob/main/packages/agent/README.md)",
			"[External](https://example.com/docs)",
			"[Local anchor](#settings)",
		}, "\n")
		want := strings.Join([]string{
			"[#5167](https://github.com/earendil-works/pi/pull/5167)",
			"[#4163](https://github.com/earendil-works/pi/issues/4163)",
			"[Agent README](https://github.com/earendil-works/pi/blob/v0.79.0/packages/agent/README.md)",
			"[External](https://example.com/docs)",
			"[Local anchor](#settings)",
		}, "\n")
		if got := NormalizeChangelogLinks(markdown, "0.79.0"); got != want {
			t.Fatalf("NormalizeChangelogLinks = %q, want %q", got, want)
		}
	})
}
