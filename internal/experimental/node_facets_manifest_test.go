package experimental

import (
	"encoding/json"
	"testing"
)

// A member that is not valid JSON must be rejected instead of spliced into the manifest, and a valid member keeps its exact bytes without HTML escaping.
func TestFacetArtifactManifestJSONEmbedsMembersAsData(t *testing.T) {
	got, err := facetArtifactManifestJSON(json.RawMessage(`{"id":"a\"b<c>"}`), json.RawMessage(`{"n\"ame":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"format":"chord.facet-bundle","formatVersion":2,"plugin":{"id":"a\"b<c>"},"entries":{"n\"ame":{}}}`
	if string(got) != want {
		t.Fatalf("manifest = %s, want %s", got, want)
	}
	for _, plugin := range []string{`{"id":"x"},"entries":{}`, `{"id":`, ``} {
		if plugin == "" {
			continue
		}
		if manifest, err := facetArtifactManifestJSON(json.RawMessage(plugin), json.RawMessage(`{}`)); err == nil {
			t.Fatalf("plugin %q accepted: %s", plugin, manifest)
		}
	}
}
