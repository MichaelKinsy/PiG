package pioracle

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// fakeOracle lays out <dir>/oracle/node_modules/.bin/pi printing version, with an optional build stamp in <dir>/oracle.
func fakeOracle(t *testing.T, version, stamp string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake oracle is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "oracle", "node_modules", ".bin", "pi")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if stamp != "" {
		if err := os.WriteFile(filepath.Join(dir, "oracle", StampFile), []byte(stamp), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

func TestCheckBinAcceptsOnlyThePinnedBuild(t *testing.T) {
	good := `{"commit":"` + pigversion.UpstreamCommit + `","source":"build"}`
	other := `{"commit":"0000000000000000000000000000000000000000","source":"build"}`
	cases := []struct {
		name, version, stamp, want string
	}{
		{"the pinned build", pigversion.UpstreamPackageVersion, good, ""},
		{"another commit of the same package version", pigversion.UpstreamPackageVersion, other, "build of Pi commit"},
		{"a package with no build stamp", pigversion.UpstreamPackageVersion, "", "not the source build"},
		{"another package version", "0.0.1", good, "version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckBin(fakeOracle(t, tc.version, tc.stamp))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("CheckBin = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckBin = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// fakeDurable lays out a repository root with the vendored manifest (the pack of pi-durable has the integrity `packIntegrity`, built from `commit`)
// and a node_modules directory whose .package-lock.json records pi-durable of `version`, installed from `resolved` with `integrity`, as npm writes it.
func fakeDurable(t *testing.T, commit, version, resolved, integrity string) (modules, root string) {
	t.Helper()
	root = t.TempDir()
	packs := filepath.Join(root, "third_party", "pi-packs")
	if err := os.MkdirAll(packs, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"commit":"` + commit + `","packs":{"earendil-works-pi-durable-1.5.2.tgz":{"name":"@earendil-works/pi-durable","integrity":"sha512-pinned"}}}`
	if err := os.WriteFile(filepath.Join(packs, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	modules = filepath.Join(root, "interop", "node_modules")
	if err := os.MkdirAll(modules, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := `{"packages":{"node_modules/@earendil-works/pi-durable":{"version":"` + version + `","resolved":"` + resolved + `","integrity":"` + integrity + `"}}}`
	if err := os.WriteFile(filepath.Join(modules, ".package-lock.json"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	return modules, root
}

// Durable's package version is the release's for a commit pin too, so durable/interop must refuse the registry's package (it lacks what the
// pin has) and accept only the pack the repository vendors for the pinned commit.
func TestCheckDurableAcceptsOnlyTheVendoredPack(t *testing.T) {
	good := "file:../../third_party/pi-packs/earendil-works-pi-durable-1.5.2.tgz"
	cases := []struct {
		name, commit, version, resolved, integrity, want string
	}{
		{"the vendored pack", pigversion.UpstreamCommit, pigversion.UpstreamPackageVersion, good, "sha512-pinned", ""},
		{"the registry's package", pigversion.UpstreamCommit, pigversion.UpstreamPackageVersion, "https://registry.npmjs.org/@earendil-works/pi-durable/-/pi-durable-1.5.2.tgz", "sha512-pinned", "not the vendored"},
		{"another build of the same version", pigversion.UpstreamCommit, pigversion.UpstreamPackageVersion, good, "sha512-other", "not the vendored"},
		{"another package version", pigversion.UpstreamCommit, "0.0.1", good, "sha512-pinned", "want " + pigversion.UpstreamPackageVersion},
		{"packs of another commit", "0000000000000000000000000000000000000000", pigversion.UpstreamPackageVersion, good, "sha512-pinned", "want " + pigversion.UpstreamCommit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modules, root := fakeDurable(t, tc.commit, tc.version, tc.resolved, tc.integrity)
			err := checkDurable(modules, root)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("checkDurable = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("checkDurable = %v, want an error containing %q", err, tc.want)
			}
		})
	}
	modules, root := fakeDurable(t, pigversion.UpstreamCommit, pigversion.UpstreamPackageVersion, good, "sha512-pinned")
	if err := os.Remove(filepath.Join(modules, ".package-lock.json")); err != nil {
		t.Fatal(err)
	}
	if err := checkDurable(modules, root); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("checkDurable of an empty tree = %v, want not installed", err)
	}
}
