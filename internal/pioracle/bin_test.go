package pioracle

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/testenv"
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

// bundledOracle lays out an oracle as automation/pin/oracle.py builds one of a Pi whose bin is a bundle: .bin/pi links to
// node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js, which imports chunks/chunk.js. It returns the bin and the bundle's digest.
func bundledOracle(t *testing.T) (bin, digest string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake oracle is a shell script behind a symlink")
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, "node_modules", "@earendil-works", "pi-coding-agent", "dist", "bundle")
	cli, chunk := "#!/bin/sh\necho "+pigversion.UpstreamPackageVersion+"\n", "export const main = 1;\n"
	for name, content := range map[string]string{"cli.js": cli, "chunks/chunk.js": chunk} {
		path := filepath.Join(bundle, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin = filepath.Join(dir, "node_modules", ".bin", "pi")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, filepath.Join(bundle, "cli.js"), bin)
	// oracle.py's tree_digest, written out: one `<sha256 of the file>  <path>` line per file, in path order.
	lines := fmt.Sprintf("%x  chunks/chunk.js\n%x  cli.js\n", sha256.Sum256([]byte(chunk)), sha256.Sum256([]byte(cli)))
	return bin, fmt.Sprintf("%x", sha256.Sum256([]byte(lines)))
}

// The post-release backports patch Pi's compiled output and pi runs a bundle of it, so an oracle that carries backports must run the bundle
// its build made from the patched output. A build that patched the compiled output only compared Pig's CLI with the unpatched release.
func TestCheckBinRefusesABackportedOracleWhosePiRunsAnotherBundleThanItsBuildStamped(t *testing.T) {
	commit := `"commit":"` + pigversion.UpstreamCommit + `","source":"build"`
	cases := []struct {
		name  string
		stamp func(digest string) string
		want  string
	}{
		{"no backports: the bundle is the pin's", func(string) string { return "{" + commit + "}" }, ""},
		{"backports and the bundle the build stamped", func(digest string) string {
			return "{" + commit + `,"backports":"b","bundle_sha256":"` + digest + `"}`
		}, ""},
		{"backports in the compiled output only", func(string) string { return "{" + commit + `,"backports":"b"}` }, "compiled output only"},
		{"catalog data and the bundle the build stamped", func(digest string) string {
			return "{" + commit + `,"catalog":"c","bundle_sha256":"` + digest + `"}`
		}, ""},
		{"catalog data in the compiled output only", func(string) string { return "{" + commit + `,"catalog":"c"}` }, "compiled output only"},
		{"catalog data and another bundle than the build stamped", func(string) string {
			return "{" + commit + `,"catalog":"c","bundle_sha256":"` + strings.Repeat("0", 64) + `"}`
		}, "its build stamped " + strings.Repeat("0", 64)},
		{"backports and another bundle than the build stamped", func(string) string {
			return "{" + commit + `,"backports":"b","bundle_sha256":"` + strings.Repeat("0", 64) + `"}`
		}, "its build stamped " + strings.Repeat("0", 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin, digest := bundledOracle(t)
			stamp := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(bin))), StampFile)
			if err := os.WriteFile(stamp, []byte(tc.stamp(digest)), 0o600); err != nil {
				t.Fatal(err)
			}
			err := CheckBin(bin)
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
