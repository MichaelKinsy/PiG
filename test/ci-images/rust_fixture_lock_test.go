package ciimages

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const (
	rustSDKLockPath     = "extensions/sdk-rs/Cargo.lock"
	rustFixtureLockPath = "test/extension-conformance/testdata/rust-sdk-fixture/Cargo.lock"
	rustFixtureCrate    = "rust-sdk-fixture"
)

// cargoLockPackage is one [[package]] table of a Cargo.lock.
type cargoLockPackage struct {
	Name         string   `toml:"name"`
	Version      string   `toml:"version"`
	Source       string   `toml:"source"`
	Checksum     string   `toml:"checksum"`
	Dependencies []string `toml:"dependencies"`
}

func (p cargoLockPackage) key() string { return p.Name + " " + p.Version }

func readCargoLock(t *testing.T, path string) map[string]cargoLockPackage {
	t.Helper()
	var lock struct {
		Package []cargoLockPackage `toml:"package"`
	}
	if _, err := toml.DecodeFile(filepath.Join(repoRoot(t), filepath.FromSlash(path)), &lock); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	packages := make(map[string]cargoLockPackage, len(lock.Package))
	for _, p := range lock.Package {
		packages[p.key()] = p
	}
	return packages
}

// fixtureLockDifferences lists where the fixture's lock departs from the SDK's: it must resolve every package the SDK resolves to the same version, source,
// checksum and dependencies, and add only the fixture crate.
func fixtureLockDifferences(sdk, fixture map[string]cargoLockPackage) []string {
	var differences []string
	for _, key := range slices.Sorted(maps.Keys(sdk)) {
		want, got := sdk[key], fixture[key]
		switch {
		case got.Name == "":
			differences = append(differences, fmt.Sprintf("%s: the SDK resolves it and the fixture does not", key))
		case got.Source != want.Source || got.Checksum != want.Checksum:
			differences = append(differences, fmt.Sprintf("%s: source or checksum differs", key))
		case !slices.Equal(slices.Sorted(slices.Values(got.Dependencies)), slices.Sorted(slices.Values(want.Dependencies))):
			differences = append(differences, fmt.Sprintf("%s: dependencies differ (SDK %v, fixture %v)", key, want.Dependencies, got.Dependencies))
		}
	}
	ownPackage := false
	for _, key := range slices.Sorted(maps.Keys(fixture)) {
		p := fixture[key]
		if p.Name == rustFixtureCrate {
			ownPackage = slices.Contains(p.Dependencies, "pig-sdk")
		} else if _, inSDK := sdk[key]; !inSDK {
			differences = append(differences, fmt.Sprintf("%s: the fixture resolves it and the SDK does not", key))
		}
	}
	if !ownPackage {
		differences = append(differences, "the fixture's own package, depending on pig-sdk, is not in its lock")
	}
	return differences
}

// Cargo hashes the resolved version of every dependency into each compiled unit's fingerprint, so the Cargo target that automation/ci/test-fixtures.sh
// exports as CARGO_TARGET_DIR holds the compiled pig_sdk that a packed-cell build links against only when the fixture build resolved the SDK's dependencies
// exactly as the cell build does (it copies the SDK's Cargo.lock into the cell). The fixture's lock, which Dependabot does not update, had stayed at
// serde_json 1.0.151 after the SDK's moved to 1.0.154: the fixture build then warmed units no cell used, and TestBuildRustPackedCellBuildsCachedRunner compiled
// serde_json and pig_sdk itself (73 s of one core on an idle host) inside the 12-way package pool of a 4-CPU runner, where the build ran past the go test
// deadline (478 s of 590 s, twice).
func TestRustFixtureLockResolvesWhatTheSDKLockResolves(t *testing.T) {
	sdk := readCargoLock(t, rustSDKLockPath)
	fixture := readCargoLock(t, rustFixtureLockPath)
	if len(sdk) == 0 || len(fixture) == 0 {
		t.Fatalf("empty lock: SDK %d packages, fixture %d", len(sdk), len(fixture))
	}
	if differences := fixtureLockDifferences(sdk, fixture); len(differences) > 0 {
		t.Fatalf("%s resolves differently from %s:\n  %s\nSync it: cp %s %s && (cd %s && cargo update --workspace)",
			rustFixtureLockPath, rustSDKLockPath, strings.Join(differences, "\n  "),
			rustSDKLockPath, rustFixtureLockPath, filepath.ToSlash(filepath.Dir(rustFixtureLockPath)))
	}
}

func TestFixtureLockDifferencesNamesEveryDeparture(t *testing.T) {
	pkg := func(name, version, checksum string, deps ...string) cargoLockPackage {
		return cargoLockPackage{Name: name, Version: version, Source: "registry+x", Checksum: checksum, Dependencies: deps}
	}
	index := func(packages ...cargoLockPackage) map[string]cargoLockPackage {
		out := make(map[string]cargoLockPackage, len(packages))
		for _, p := range packages {
			out[p.key()] = p
		}
		return out
	}
	fixtureCrate := cargoLockPackage{Name: rustFixtureCrate, Version: "0.1.0", Dependencies: []string{"pig-sdk", "serde_json"}}
	sdk := index(pkg("serde_json", "1.0.154", "new", "foldhash", "itoa"), pkg("foldhash", "0.2.0", "f"))
	for _, tc := range []struct {
		name    string
		fixture map[string]cargoLockPackage
		want    string
	}{
		{"matching lock", index(fixtureCrate, pkg("foldhash", "0.2.0", "f"), pkg("serde_json", "1.0.154", "new", "itoa", "foldhash")), ""},
		{"older version", index(fixtureCrate, pkg("foldhash", "0.2.0", "f"), pkg("serde_json", "1.0.151", "old", "itoa")), "serde_json 1.0.154: the SDK resolves it and the fixture does not"},
		{"package the SDK dropped", index(fixtureCrate, pkg("foldhash", "0.2.0", "f"), pkg("serde_json", "1.0.154", "new", "foldhash", "itoa"), pkg("ryu", "1.0.0", "r")), "ryu 1.0.0: the fixture resolves it and the SDK does not"},
		{"other checksum", index(fixtureCrate, pkg("foldhash", "0.2.0", "g"), pkg("serde_json", "1.0.154", "new", "foldhash", "itoa")), "foldhash 0.2.0: source or checksum differs"},
		{"other dependencies", index(fixtureCrate, pkg("foldhash", "0.2.0", "f"), pkg("serde_json", "1.0.154", "new", "itoa")), "serde_json 1.0.154: dependencies differ"},
		{"no fixture package", index(pkg("foldhash", "0.2.0", "f"), pkg("serde_json", "1.0.154", "new", "foldhash", "itoa")), "the fixture's own package"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(fixtureLockDifferences(sdk, tc.fixture), "\n")
			if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
				t.Fatalf("differences = %q, want %q", got, tc.want)
			}
		})
	}
}
