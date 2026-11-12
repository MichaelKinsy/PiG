package pioracle

import (
	"encoding/json"
	"errors"
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

// vendoredPacks is where the pinned build's `npm pack` outputs live, relative to the repository root (automation/pin/vendor.py).
const vendoredPacks = "third_party/pi-packs"

// CheckDurable reports why modules, a node_modules directory such as durable/interop's, does not hold the pinned Pi Durable: the pack the
// repository vendors for the pinned commit (third_party/pi-packs), which `npm ci` installs from the lock's file: dependency. npm records
// each installed package's source and integrity in modules/.package-lock.json; they must be the manifest's.
func CheckDurable(modules string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return checkDurable(modules, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return errors.New("pioracle: go.mod not found above the working directory")
		}
		dir = parent
	}
}

func checkDurable(modules, root string) error {
	return checkDurableOf(modules, root, pigversion.UpstreamCommit, pigversion.UpstreamPackageVersion)
}

// checkDurableOf requires modules to hold the Pi Durable of the packs under root, which must be the build of commit; version, when
// given, is the package version the installed Pi Durable must report.
func checkDurableOf(modules, root, commit, version string) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(vendoredPacks), "manifest.json"))
	if err != nil {
		return fmt.Errorf("pioracle: %w (make vendor-pi-packs writes it)", err)
	}
	var manifest struct {
		Commit string `json:"commit"`
		Packs  map[string]struct {
			Name, Integrity string
		} `json:"packs"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("pioracle: %s manifest: %w", vendoredPacks, err)
	}
	if manifest.Commit != commit {
		return fmt.Errorf("pioracle: %s holds the build of Pi commit %s, want %s (make vendor-pi-packs)", filepath.Join(root, filepath.FromSlash(vendoredPacks)), manifest.Commit, commit)
	}
	installed, err := os.ReadFile(filepath.Join(modules, ".package-lock.json"))
	if err != nil {
		return fmt.Errorf("pioracle: Pi Durable is not installed in %s (make durable-interop-deps installs it): %w", modules, err)
	}
	var lock struct {
		Packages map[string]struct{ Version, Resolved, Integrity string } `json:"packages"`
	}
	if err := json.Unmarshal(installed, &lock); err != nil {
		return fmt.Errorf("pioracle: %s: %w", modules, err)
	}
	got, ok := lock.Packages["node_modules/@earendil-works/pi-durable"]
	if !ok {
		return fmt.Errorf("pioracle: Pi Durable is not installed in %s (make durable-interop-deps installs it)", modules)
	}
	for file, pack := range manifest.Packs {
		if pack.Name != "@earendil-works/pi-durable" {
			continue
		}
		if version != "" && got.Version != version {
			return fmt.Errorf("pioracle: %s holds Pi Durable %s, want %s", modules, got.Version, version)
		}
		if !strings.HasSuffix(got.Resolved, "/"+vendoredPacks+"/"+file) || got.Integrity != pack.Integrity {
			return fmt.Errorf("pioracle: %s holds Pi Durable from %q (%s), not the vendored %s/%s (%s): make durable-interop-deps installs it", modules, got.Resolved, got.Integrity, vendoredPacks, file, pack.Integrity)
		}
		return nil
	}
	return fmt.Errorf("pioracle: %s/manifest.json lists no pack of @earendil-works/pi-durable", vendoredPacks)
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
