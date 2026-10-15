// SPDX-License-Identifier: MIT

// The ceilings are measured on linux; other platforms allocate differently on their file, terminal and process paths.

//go:build linux

package perfgate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
