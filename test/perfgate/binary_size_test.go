// SPDX-License-Identifier: MIT

// The ceilings are measured on linux; other platforms allocate differently on their file, terminal and process paths.

//go:build linux

package perfgate

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// releasePlatforms are the stripped release builds whose size is gated; release-candidate.yml builds them with CGO_ENABLED=0, -trimpath and -ldflags "-s -w".
var releasePlatforms = []struct{ goos, goarch string }{
	{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}, {"windows", "amd64"},
}

// The stock pig binary of each release platform, stripped as released, may only shrink.
func TestReleaseBinarySizeGate(t *testing.T) {
	root := testenv.ModuleRoot(t)
	for _, platform := range releasePlatforms {
		t.Run(platform.goos+"_"+platform.goarch, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "pig")
			build := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w", "-o", out, "./cmd/pig")
			build.Dir = root
			build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+platform.goos, "GOARCH="+platform.goarch)
			if data, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, data)
			}
			info, err := os.Stat(out)
			if err != nil {
				t.Fatal(err)
			}
			perfgate.Check(t, "binary.pig."+platform.goos+"_"+platform.goarch+".bytes", float64(info.Size()))
		})
	}
}

// The headless embed example (examples/headless: the session API, MCP and one provider, no terminal) of each release
// platform, stripped as released, may only shrink. "default" is a plain build, what an embedder gets without build tags.
// "stripped" passes every strip tag except the one that removes MCP, the set docs/site/docs/sdk.md recommends for an
// embedder that has no use for the features they name.
func TestHeadlessEmbedBinarySizeGate(t *testing.T) {
	root := testenv.ModuleRoot(t)
	stripped := slices.DeleteFunc(piglet.StripBuildTags(), func(tag string) bool { return tag == "pig_strip_mcp" })
	variants := []struct{ name, tags string }{{"default", ""}, {"stripped", strings.Join(stripped, ",")}}
	for _, variant := range variants {
		for _, platform := range releasePlatforms {
			t.Run(variant.name+"_"+platform.goos+"_"+platform.goarch, func(t *testing.T) {
				out := filepath.Join(t.TempDir(), "headless")
				args := []string{"build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w", "-o", out}
				if variant.tags != "" {
					args = append(args, "-tags", variant.tags)
				}
				build := exec.CommandContext(t.Context(), "go", append(args, "./examples/headless")...)
				build.Dir = root
				build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+platform.goos, "GOARCH="+platform.goarch)
				if data, err := build.CombinedOutput(); err != nil {
					t.Fatalf("go build: %v\n%s", err, data)
				}
				info, err := os.Stat(out)
				if err != nil {
					t.Fatal(err)
				}
				perfgate.Check(t, "binary.headless."+variant.name+"."+platform.goos+"_"+platform.goarch+".bytes", float64(info.Size()))
			})
		}
	}
}
