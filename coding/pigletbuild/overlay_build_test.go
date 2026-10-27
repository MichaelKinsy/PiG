package pigletbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
)

// The container builder bakes the localized Piglet inside the container, so the localized Piglet must carry
// build.allowOverlays (D202) and nothing else of build.
func TestContainerLocalizedPigletKeepsAllowOverlays(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build string
		want  bool
	}{
		{"set", "build:\n  allowOverlays: true\n  outputName: pig-x\n  targets: [linux/amd64]\n", true},
		{"unset", "build:\n  outputName: pig-x\n", false},
		{"no build", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "x.yaml")
			if err := os.WriteFile(path, []byte("name: x\nmodel:\n  provider: test-faux\n  name: faux-1\n"+tc.build), 0o644); err != nil {
				t.Fatal(err)
			}
			parsed, err := piglet.Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			localized, _, err := localizeContainerPiglet(parsed)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(localized), "allowOverlays: true"); got != tc.want {
				t.Fatalf("localized Piglet carries allowOverlays = %t, want %t:\n%s", got, tc.want, localized)
			}
			if strings.Contains(string(localized), "outputName") || strings.Contains(string(localized), "targets") {
				t.Fatalf("localized Piglet kept other build settings:\n%s", localized)
			}
			if _, err := piglet.ParseBytes(localized); err != nil {
				t.Fatal(err)
			}
		})
	}
}
