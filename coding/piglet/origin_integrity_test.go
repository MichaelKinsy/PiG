package piglet

import (
	"crypto/sha512"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// npm names a lockfile entry by the path from the install root as given to the
// real path of the package, so an install root reached through a symlink (macOS
// /var -> /private/var) yields keys such as ../../real/inst/node_modules/@sc/pkg.
func TestReadNPMOriginIntegrityResolvesLockfileKeys(t *testing.T) {
	const integrity = "sha512-fixture"
	other := `"node_modules/other":{"version":"1.0.0","integrity":"sha512-other"}`
	cases := []struct {
		name string
		key  string
		// linked installs the package through a symlinked install root.
		linked bool
		// viaLink names the materialized root through the symlink instead of the real path.
		viaLink bool
		want    string
	}{
		{name: "plain key", key: "node_modules/@sc/pkg", want: integrity},
		{name: "symlinked root, npm real-path key, root given as link", key: "../../real/inst/node_modules/@sc/pkg", linked: true, viaLink: true, want: integrity},
		{name: "symlinked root, npm real-path key, root given as real path", key: "../../real/inst/node_modules/@sc/pkg", linked: true, want: integrity},
		{name: "symlinked root, plain key", key: "node_modules/@sc/pkg", linked: true, viaLink: true, want: integrity},
		{name: "no entry for the package", key: "node_modules/@sc/unrelated", want: ""},
		{name: "real-path key of another package", key: "../../real/inst/node_modules/@sc/unrelated", linked: true, viaLink: true, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			if resolved, err := filepath.EvalSymlinks(base); err == nil {
				base = resolved
			}
			realInstall := filepath.Join(base, "real", "inst")
			packageRoot := filepath.Join(realInstall, "node_modules", "@sc", "pkg")
			if err := os.MkdirAll(packageRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			installRoot := realInstall
			if tc.linked {
				testenv.RequireDirectoryLink(t, filepath.Join(base, "real"), filepath.Join(base, "link"))
				installRoot = filepath.Join(base, "link", "inst")
			}
			lock := `{"lockfileVersion":3,"packages":{"":{},` + other + `,"` + tc.key + `":{"version":"1.0.0","integrity":"` + integrity + `"}}}`
			if err := os.WriteFile(filepath.Join(realInstall, "package-lock.json"), []byte(lock), 0o644); err != nil {
				t.Fatal(err)
			}
			materialized := packageRoot
			if tc.viaLink {
				materialized = filepath.Join(installRoot, "node_modules", "@sc", "pkg")
			}
			if got := readNPMOriginIntegrity(materialized); got != tc.want {
				t.Fatalf("readNPMOriginIntegrity(%q) = %q, want %q", materialized, got, tc.want)
			}
		})
	}
}

// The real npm CLI installs a packed tarball under an install root reached through a symlink, as a symlinked agent directory does on any platform. The origin integrity is the tarball's own SHA-512, not a value read back from the fixture.
func TestReadNPMOriginIntegrityFromRealNPMThroughSymlinkedPrefix(t *testing.T) {
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Fatalf("npm is required: %v", err)
	}
	base := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	source := filepath.Join(base, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"name":"@sc/pkg","version":"1.0.0","license":"MIT"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	realInstall := filepath.Join(base, "real", "inst")
	if err := os.MkdirAll(realInstall, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, filepath.Join(base, "real"), filepath.Join(base, "link"))
	linkedInstall := filepath.Join(base, "link", "inst")
	run := func(dir string, args ...string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), npm, args...)
		command.Dir = dir
		command.Env = append(os.Environ(), "npm_config_cache="+filepath.Join(base, "cache"), "npm_config_userconfig="+filepath.Join(base, "npmrc"), "npm_config_update_notifier=false", "npm_config_audit=false", "npm_config_fund=false")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("npm %v: %v\n%s", args, err, output)
		}
	}
	run(source, "pack", "--pack-destination", base)
	tarball, err := os.ReadFile(filepath.Join(base, "sc-pkg-1.0.0.tgz"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha512.Sum512(tarball)
	want := "sha512-" + base64.StdEncoding.EncodeToString(digest[:])
	run(base, "install", "--prefix", linkedInstall, "--offline", filepath.Join(base, "sc-pkg-1.0.0.tgz"))
	for _, root := range []string{linkedInstall, realInstall} {
		materialized := filepath.Join(root, "node_modules", "@sc", "pkg")
		if got := readNPMOriginIntegrity(materialized); got != want {
			lock, _ := os.ReadFile(filepath.Join(realInstall, "package-lock.json"))
			t.Fatalf("readNPMOriginIntegrity(%q) = %q, want %q\npackage-lock.json:\n%s", materialized, got, want, lock)
		}
	}
}
