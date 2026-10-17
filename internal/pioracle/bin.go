package pioracle

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// StampFile is the build stamp automation/pin/oracle.py writes beside an oracle's node_modules.
const StampFile = ".pig-pi-oracle.json"

// Bin returns the Pi executable a test compares Pig with: PIG_PARITY_PI_BIN, else the oracle `make parity-deps` built from source at the pinned commit and linked into extensions/sdk-ts. The test is skipped when it is absent and fails when it is not the pinned build.
func Bin(t testing.TB) string {
	t.Helper()
	bin := os.Getenv("PIG_PARITY_PI_BIN")
	if bin == "" {
		bin = filepath.Join(repoRoot(t), "extensions", "sdk-ts", "node_modules", ".bin", "pi")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("the Pi comparator is not built at %s; run make parity-deps (builds Pi from source at pinned commit %s)", bin, pigversion.UpstreamCommit)
	}
	if err := CheckBin(bin); err != nil {
		t.Fatal(err)
	}
	return bin
}

// CheckBin reports why bin is not the Pi built from the pinned commit. The version string alone cannot tell two commits of one package version apart, so the build stamp must name the pinned commit.
func CheckBin(bin string) error {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return fmt.Errorf("pioracle: %s --version failed: %w", bin, err)
	}
	if got := strings.TrimSpace(string(out)); got != pigversion.UpstreamPackageVersion {
		return fmt.Errorf("pioracle: %s reports version %s, want %s", bin, got, pigversion.UpstreamPackageVersion)
	}
	return checkStamp(bin, "make parity-deps")
}

// CheckDurable reports why modules, a node_modules directory such as durable/interop's, does not hold the pinned Pi Durable. A release pin
// installs the published package (`make durable-interop-deps` runs npm-locked.py), so its version is enough. A commit pin shares that
// package version with the release, so the tree must be the build of the pinned commit that `pin.py install-oracle` stamps.
func CheckDurable(modules string) error {
	data, err := os.ReadFile(filepath.Join(modules, "@earendil-works", "pi-durable", "package.json"))
	if err != nil {
		return fmt.Errorf("pioracle: Pi Durable is not installed in %s (make durable-interop-deps installs it): %w", modules, err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return fmt.Errorf("pioracle: %s: %w", modules, err)
	}
	if pkg.Version != pigversion.UpstreamPackageVersion {
		return fmt.Errorf("pioracle: %s holds Pi Durable %s, want %s", modules, pkg.Version, pigversion.UpstreamPackageVersion)
	}
	if pigversion.UpstreamVersion == pigversion.UpstreamPackageVersion {
		return nil
	}
	return checkStamp(modules, "make durable-interop-deps")
}

// checkStamp finds the build stamp in path's real directory or above it and requires it to name the pinned commit.
func checkStamp(path, install string) error {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	for dir := filepath.Dir(real); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, StampFile))
		if err != nil {
			continue
		}
		var stamp struct {
			Commit string `json:"commit"`
		}
		if json.Unmarshal(data, &stamp) != nil || stamp.Commit != pigversion.UpstreamCommit {
			return fmt.Errorf("pioracle: %s is the build of Pi commit %q, want %s", path, stamp.Commit, pigversion.UpstreamCommit)
		}
		return nil
	}
	return fmt.Errorf("pioracle: %s is a published package, not the source build of pinned commit %s (%s builds it)", path, pigversion.UpstreamCommit, install)
}

func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("pioracle: go.mod not found above the test directory")
		}
		dir = parent
	}
}
