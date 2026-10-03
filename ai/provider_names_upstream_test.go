package ai

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// providerNamePattern reads the first `name: "..."` of a provider source after its `id: "..."`.
var providerNamePattern = regexp.MustCompile(`(?s)\bid: "([^"]+)",\s*name: "([^"]+)"`)

// Pi 0.99.2 packages/ai/src/providers/<id>.ts createProvider({ id, name }): the provider name the login
// selector (interactive-mode.ts getLoginProviderOptions) and its status labels show. Every provider of the
// catalog barrel must take its name from the pinned source (.upstream/v<UpstreamVersion>, not the mutable current symlink); openai-codex.ts:10 names itself "OpenAI Codex (legacy)".
func TestProviderDisplayNamesMatchTheUpstreamProviderSources(t *testing.T) {
	for _, id := range GeneratedProviders {
		source := filepath.Join("..", ".upstream", "v"+UpstreamVersionString(), "packages", "ai", "src", "providers", id+".ts")
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		match := providerNamePattern.FindSubmatch(body)
		if match == nil {
			// radius.ts builds its name from options.name ?? "Radius" (radius.ts:24).
			if id == "radius" {
				if got := ProviderDisplayName(id); got != "Radius" {
					t.Errorf("ProviderDisplayName(%q) = %q, want Radius", id, got)
				}
				continue
			}
			t.Fatalf("%s: no id/name pair in %s", id, source)
		}
		if string(match[1]) != id {
			t.Fatalf("%s: source declares id %q", id, match[1])
		}
		if got, want := ProviderDisplayName(id), string(match[2]); got != want {
			t.Errorf("ProviderDisplayName(%q) = %q, want %q (providers/%s.ts)", id, got, want, id)
		}
	}
}
