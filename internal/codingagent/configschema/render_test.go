package configschema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/configschema"
)

// The artifacts Pig renders are byte-equal to the schemas the pinned Pi package ships (its generate-schemas.ts output), so a pin bump that
// changes a TypeBox schema fails here until the Go trees follow.
func TestRenderedSchemasAreByteEqualToThePinnedPiPackage(t *testing.T) {
	pi := filepath.Join(repoRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	var manifest struct{ Version string }
	data, err := os.ReadFile(filepath.Join(pi, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != pigversion.UpstreamPackageVersion {
		t.Fatalf("Pi oracle version %s, want %s", manifest.Version, pigversion.UpstreamPackageVersion)
	}
	rendered, err := configschema.Render(codingagent.KeybindingsSchemaArtifact())
	if err != nil {
		t.Fatal(err)
	}
	for path, got := range rendered {
		want, err := os.ReadFile(filepath.Join(pi, path))
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s differs from the pinned Pi package", path)
		}
	}
}

// The same byte equality against Pi's own schemas/ directory in the upstream mirror (scripts/generate-schemas.ts output at the pin),
// which needs no installed Pi package.
func TestRenderedSchemasAreByteEqualToTheUpstreamMirror(t *testing.T) {
	pi := filepath.Join(repoRoot(t), ".upstream", "current", "packages", "coding-agent")
	rendered, err := configschema.Render(codingagent.KeybindingsSchemaArtifact())
	if err != nil {
		t.Fatal(err)
	}
	for path, got := range rendered {
		want, err := os.ReadFile(filepath.Join(pi, path))
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s differs from Pi's %s", path, filepath.Join(".upstream/current/packages/coding-agent", path))
		}
	}
}
