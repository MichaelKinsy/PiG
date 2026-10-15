package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// The image's references are Pi's packages at PiG's pinned Pi version: pi-durable for the Durable profiles and the
// coding agent for the agent profiles. An upstream leap must move them too.
func TestImageReferenceIsThePinnedPiVersion(t *testing.T) {
	for dir, names := range map[string][]string{
		"pi-durable": {"@earendil-works/pi-durable", "@earendil-works/pi-ai", "@earendil-works/chord"},
		"pi-agent":   {"@earendil-works/pi-coding-agent"},
	} {
		dir := filepath.Join("..", "..", "image", dir)
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
		for _, name := range names {
			if got := manifest.Dependencies[name]; got != pigversion.UpstreamPackageVersion {
				t.Errorf("%s: package.json pins %s %q, want the pinned Pi version %s", dir, name, got, pigversion.UpstreamPackageVersion)
			}
			if got := lock.Packages["node_modules/"+name].Version; got != pigversion.UpstreamPackageVersion {
				t.Errorf("%s: package-lock.json resolves %s %q, want %s; regenerate it with npm install --package-lock-only", dir, name, got, pigversion.UpstreamPackageVersion)
			}
		}
	}
}
