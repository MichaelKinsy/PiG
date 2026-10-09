package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// The image's reference is pi-durable at PiG's pinned Pi version; an upstream leap must move it too.
func TestImageReferenceIsThePinnedPiVersion(t *testing.T) {
	dir := filepath.Join("..", "..", "image", "pi-durable")
	var manifest struct{ Dependencies map[string]string }
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct{ Version string } `json:"packages"`
	}
	data, err = os.ReadFile(filepath.Join(dir, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"@earendil-works/pi-durable", "@earendil-works/pi-ai", "@earendil-works/chord"} {
		if got := manifest.Dependencies[name]; got != pigversion.UpstreamVersion {
			t.Errorf("package.json pins %s %q, want the pinned Pi version %s", name, got, pigversion.UpstreamVersion)
		}
		if got := lock.Packages["node_modules/"+name].Version; got != pigversion.UpstreamVersion {
			t.Errorf("package-lock.json resolves %s %q, want %s; regenerate it with npm install --package-lock-only", name, got, pigversion.UpstreamVersion)
		}
	}
}
