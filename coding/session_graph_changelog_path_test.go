package coding

import (
	"encoding/json"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// package-manager.ts PathMetadata.changelogPath (b4b72a35) crosses the session graph wire both ways: a resolved resource carries it to the SDK, and
// extendResources hands it back to the loader.
func TestSessionGraphCarriesTheChangelogPath(t *testing.T) {
	resolved := graphResolved([]icodingagent.ResolvedResource{
		{Path: "/p/a.ts", Enabled: true, Metadata: icodingagent.PathMetadata{Source: "npm:p", Scope: "user", Origin: "package", BaseDir: "/p", ChangelogPath: "/p/CHANGELOG.md"}},
		{Path: "/p/b.ts", Enabled: true, Metadata: icodingagent.PathMetadata{Source: "local", Scope: "user", Origin: "top-level"}},
	})
	metadata := resolved[0]["metadata"].(map[string]any)
	if metadata["changelogPath"] != "/p/CHANGELOG.md" {
		t.Fatalf("metadata = %v, want the changelogPath", metadata)
	}
	if _, has := resolved[1]["metadata"].(map[string]any)["changelogPath"]; has {
		t.Fatalf("a resource without a changelog carries one: %v", resolved[1])
	}
	var decoded graphResourcePath
	if err := json.Unmarshal([]byte(`{"path":"/p/a.ts","metadata":{"source":"npm:p","scope":"user","origin":"package","baseDir":"/p","changelogPath":"/p/CHANGELOG.md"}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Metadata.ChangelogPath != "/p/CHANGELOG.md" {
		t.Fatalf("decoded = %+v, want the changelogPath", decoded)
	}
}
