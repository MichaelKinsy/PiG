package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha1" //nolint:gosec // npm packuments carry a SHA-1 shasum beside the SHA-512 integrity npm verifies.
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// npmReleaseTargets are the release archives pack_npm.py reads: GOOS/GOARCH
// release names and the npm os/cpu of the platform package each one becomes.
var npmReleaseTargets = []struct{ goos, goarch, npmOS, npmCPU string }{
	{"darwin", "amd64", "darwin", "x64"},
	{"darwin", "arm64", "darwin", "arm64"},
	{"linux", "amd64", "linux", "x64"},
	{"linux", "arm64", "linux", "arm64"},
	{"windows", "amd64", "win32", "x64"},
	{"windows", "arm64", "win32", "arm64"},
}

// localNpmRegistry serves packuments and tarballs to a real npm client and
// records every packument request.
type localNpmRegistry struct {
	server   *httptest.Server
	mu       sync.Mutex
	packages map[string]map[string]any // name -> version -> version manifest
	tarballs map[string][]byte
	requests []string
}

func newLocalNpmRegistry(t *testing.T) *localNpmRegistry {
	t.Helper()
	r := &localNpmRegistry{packages: map[string]map[string]any{}, tarballs: map[string][]byte{}}
	r.server = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.server.Close)
	return r
}

func (r *localNpmRegistry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if file, ok := strings.CutPrefix(req.URL.Path, "/-/tarballs/"); ok {
		data, found := r.tarballs[file]
		if !found {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(data)
		return
	}
	name := strings.TrimPrefix(req.URL.Path, "/")
	r.requests = append(r.requests, name)
	versions, found := r.packages[name]
	if !found {
		http.NotFound(w, req)
		return
	}
	latest := ""
	times := map[string]string{"created": "2020-01-01T00:00:00.000Z", "modified": "2020-01-01T00:00:00.000Z"}
	for _, version := range slices.Sorted(func(yield func(string) bool) {
		for v := range versions {
			if !yield(v) {
				return
			}
		}
	}) {
		latest = version
		times[version] = "2020-01-01T00:00:00.000Z"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "dist-tags": map[string]string{"latest": latest}, "versions": versions, "time": times})
}

// publish packs the package directory dir as npm does ("package/" entries)
// and serves it under its package.json name and version.
func (r *localNpmRegistry) publish(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	name, version := manifest["name"].(string), manifest["version"].(string)
	tarball := npmTarball(t, dir)
	file := strings.NewReplacer("@", "", "/", "-").Replace(name) + "-" + version + ".tgz"
	sum512 := sha512.Sum512(tarball)
	sum1 := sha1.Sum(tarball) //nolint:gosec // registry metadata field, not a security check.
	manifest["dist"] = map[string]string{
		"tarball":   r.server.URL + "/-/tarballs/" + file,
		"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum512[:]),
		"shasum":    hex.EncodeToString(sum1[:]),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.packages[name] == nil {
		r.packages[name] = map[string]any{}
	}
	r.packages[name][version] = manifest
	r.tarballs[file] = tarball
}

func (r *localNpmRegistry) requested() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

func npmTarball(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: path.Join("package", filepath.ToSlash(rel)), Mode: int64(info.Mode().Perm()), Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// packNpmRelease builds the six release archives for version (the host
// target's binary is hostBinary; the others hold placeholder bytes), runs the
// release packer automation/release/npm/pack_npm.py on them, and publishes the
// seven packages it generates to the registry.
func packNpmRelease(t *testing.T, python string, registry *localNpmRegistry, version string, hostBinary []byte) {
	t.Helper()
	work := t.TempDir()
	archives := filepath.Join(work, "archives")
	if err := os.MkdirAll(archives, 0o755); err != nil {
		t.Fatal(err)
	}
	var sums strings.Builder
	for _, target := range npmReleaseTargets {
		prefix := "pig-" + version + "-" + target.goos + "-" + target.goarch
		binary, content := "pig", []byte("placeholder "+prefix)
		if target.goos == "windows" {
			binary = "pig.exe"
		}
		if target.goos == runtime.GOOS && target.goarch == runtime.GOARCH {
			content = hostBinary
		}
		members := map[string][]byte{binary: content, "LICENSE": []byte("license"), "NOTICE": []byte("notice"), "THIRD_PARTY_NOTICES.md": []byte("notices")}
		var archive []byte
		var name string
		if target.goos == "windows" {
			name, archive = prefix+".zip", zipArchive(t, prefix, members)
		} else {
			name, archive = prefix+".tar.gz", tarGzArchive(t, prefix, members)
		}
		if err := os.WriteFile(filepath.Join(archives, name), archive, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(archive)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	if err := os.WriteFile(filepath.Join(archives, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(work, "npm")
	script := filepath.Join(fixtureSourceRoot, "automation", "release", "npm", "pack_npm.py")
	if data, err := exec.Command(python, script, "--archives", archives, "--version", version, "--out", out, "--no-pack").CombinedOutput(); err != nil {
		t.Fatalf("pack_npm.py: %v\n%s", err, data)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(npmReleaseTargets)+1 {
		t.Fatalf("pack_npm.py generated %d packages, want %d", len(entries), len(npmReleaseTargets)+1)
	}
	for _, entry := range entries {
		registry.publish(t, filepath.Join(out, entry.Name()))
	}
}

// npmInvocations returns the arguments of every npm run that logged to the
// cache, in order.
func npmInvocations(t *testing.T, cache string) []string {
	t.Helper()
	logs, err := filepath.Glob(filepath.Join(cache, "_logs", "*-debug-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(logs)
	var runs []string
	for _, log := range logs {
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(data)) {
			if _, args, ok := strings.Cut(line, " verbose argv "); ok {
				runs = append(runs, strings.TrimSpace(args))
			}
		}
	}
	return runs
}

func tarGzArchive(t *testing.T, prefix string, members map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range members {
			if !yield(n) {
				return
			}
		}
	}) {
		if err := tw.WriteHeader(&tar.Header{Name: prefix + "/" + name, Mode: 0o755, Size: int64(len(members[name])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(members[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipArchive(t *testing.T, prefix string, members map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range members {
		w, err := zw.Create(prefix + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// isolatedNpmBin returns a directory that holds node and an npm which runs the installed npm's own CLI script, so a version
// manager's wrapper around npm (mise's npm wrapper runs `mise reshim` after every global install) never runs in the test.
func isolatedNpmBin(t *testing.T, nodeExec, work string) string {
	t.Helper()
	prefix := filepath.Dir(filepath.Dir(nodeExec))
	cli := filepath.Join(prefix, "lib", "node_modules", "npm", "bin", "npm-cli.js")
	if resolved, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(nodeExec), "npm")); err == nil && filepath.Base(resolved) == "npm-cli.js" {
		cli = resolved
	}
	if _, err := os.Stat(cli); err != nil {
		t.Fatalf("npm's CLI script is not beside node: %v", err)
	}
	bin := filepath.Join(work, "toolchain-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, nodeExec, filepath.Join(bin, "node"))
	script := "#!/bin/sh\nexec " + shellQuote(filepath.Join(bin, "node")) + " " + shellQuote(cli) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }

// The complete npm channel with real npm and no network: the release packer's
// seven packages are served by a local registry, real npm installs
// @pi-in-go/pig into a temporary global prefix (npm nests the platform package
// that holds the native binary inside the launcher), and `pig update --self`
// run through the npm launcher reads a signed manifest from the release
// generator. PiG must identify the launcher as the installed package and have
// npm install the launcher's new release, never the unrelated unscoped `pig`
// package (upstream config.ts getSelfUpdateCommandForMethod "npm" with
// package-manager-cli.ts getSelfUpdatePlan: no uninstall step when the release
// names the installed package).
func TestNpmInstalledPigUpdatesThroughRealNpm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the npm global layout and launcher shim differ on Windows; TestWindowsNpmInstalledPigUpdatesThroughRealNpm covers that path")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	// Version managers resolve node through HOME and wrap npm with hooks of their own; the isolated HOME and PATH below
	// hold the same node and npm CLI script and none of those hooks.
	execPath, err := exec.Command("node", "-p", "process.execPath").Output()
	if err != nil {
		t.Fatalf("node -p process.execPath: %v", err)
	}
	work := t.TempDir()
	nodeBin := isolatedNpmBin(t, strings.TrimSpace(string(execPath)), work)
	pig := buildPigBinaryForSignalTest(t)
	pigBytes, err := os.ReadFile(pig)
	if err != nil {
		t.Fatal(err)
	}

	registry := newLocalNpmRegistry(t)
	packNpmRelease(t, python, registry, "0.0.1", pigBytes)
	newBinary := []byte("#!/bin/sh\necho released 9.9.9\n")
	packNpmRelease(t, python, registry, "9.9.9", newBinary)
	// The unscoped npm package "pig" is not PiG. If the update ever asks for
	// it, this registry answers, as the public registry would.
	decoy := t.TempDir()
	if err := os.WriteFile(filepath.Join(decoy, "package.json"), []byte(`{"name":"pig","version":"9.9.9","bin":{"pig-decoy":"decoy.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "decoy.js"), []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	registry.publish(t, decoy)

	prefix := filepath.Join(work, "prefix")
	env := []string{
		"HOME=" + filepath.Join(work, "home"),
		"PATH=" + nodeBin + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin",
		"npm_config_registry=" + registry.server.URL + "/",
		"npm_config_prefix=" + prefix,
		"npm_config_cache=" + filepath.Join(work, "npm-cache"),
		"npm_config_userconfig=" + filepath.Join(work, "npmrc"),
		"npm_config_globalconfig=" + filepath.Join(work, "global-npmrc"),
		"npm_config_audit=false",
		"npm_config_fund=false",
		"npm_config_update_notifier=false",
	}
	npmInstall := exec.Command(filepath.Join(nodeBin, "npm"), "install", "-g", "@pi-in-go/pig@0.0.1")
	npmInstall.Env = env
	if data, err := npmInstall.CombinedOutput(); err != nil {
		t.Fatalf("npm install -g @pi-in-go/pig@0.0.1: %v\n%s", err, data)
	}
	host := ""
	for _, target := range npmReleaseTargets {
		if target.goos == runtime.GOOS && target.goarch == runtime.GOARCH {
			host = "pig-" + target.npmOS + "-" + target.npmCPU
		}
	}
	if host == "" {
		t.Skipf("PiG ships no npm package for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	launcher := filepath.Join(prefix, "lib", "node_modules", "@pi-in-go", "pig")
	native := filepath.Join(launcher, "node_modules", "@pi-in-go", host, "pig")
	if data, err := os.ReadFile(native); err != nil || !bytes.Equal(data, pigBytes) {
		t.Fatalf("npm did not nest the platform package's binary at %s: %v", native, err)
	}

	generated := t.TempDir()
	if err := os.WriteFile(filepath.Join(generated, "pig-"+runtime.GOOS+"-"+runtime.GOARCH), newBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := exec.Command(python, filepath.Join(fixtureSourceRoot, "automation", "release", "gen-update-manifest.py"),
		"--version", "9.9.9", "--base-url", "https://updates.example", "--dir", generated).Output()
	if err != nil {
		t.Fatalf("gen-update-manifest.py: %v", err)
	}
	source := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(manifest) }))
	defer source.Close()

	update := exec.Command(filepath.Join(prefix, "bin", "pig"), "update", "--self")
	update.Dir = work
	update.Env = slices.Concat(env, []string{
		"PIG_HOME=" + filepath.Join(work, "pig-home"),
		"PIG_CODING_AGENT_DIR=" + filepath.Join(work, "agent"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(work, "pi-agent"),
		"PIG_UPDATE_URL=" + source.URL,
		"PIG_UPDATE_TRUST_ROOT=" + os.Getenv("PIG_UPDATE_TRUST_ROOT"),
		"PIG_UPDATE_ALLOW_LOOPBACK_HTTP=1",
	})
	output, err := update.CombinedOutput()
	if err != nil {
		t.Fatalf("pig update --self: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Updated to pig 9.9.9.") {
		t.Fatalf("pig update --self output:\n%s", output)
	}
	if data, err := os.ReadFile(native); err != nil || !bytes.Equal(data, newBinary) {
		t.Fatalf("npm did not install the 9.9.9 platform binary at %s: %v\n%s", native, err, output)
	}
	var installed struct{ Version string }
	if data, err := os.ReadFile(filepath.Join(launcher, "package.json")); err != nil || json.Unmarshal(data, &installed) != nil || installed.Version != "9.9.9" {
		t.Fatalf("launcher package after update = %+v (%v)\n%s", installed, err, output)
	}
	// npm logs each invocation's arguments; the update ran exactly these.
	if got, want := npmInvocations(t, filepath.Join(work, "npm-cache")), []string{
		`"install" "--global" "@pi-in-go/pig@0.0.1"`,
		`"root" "--global"`,
		`"--prefix" "` + prefix + `" "install" "--global" "--ignore-scripts" "--min-release-age" "0" "@pi-in-go/pig@9.9.9"`,
	}; !slices.Equal(got, want) {
		t.Fatalf("npm invocations:\n got: %q\nwant: %q\n%s", got, want, output)
	}
	if slices.Contains(registry.requested(), "pig") {
		t.Fatalf("the update asked the registry for the unscoped package pig: %q\n%s", registry.requested(), output)
	}
	if _, err := os.Stat(filepath.Join(prefix, "lib", "node_modules", "pig")); !os.IsNotExist(err) {
		t.Fatalf("the update installed the unscoped package pig: %v\n%s", err, output)
	}
}

// A version manager wraps npm in bin/npm and puts its own hook on PATH; the test toolchain must run npm's CLI script
// directly and keep the wrapper off PATH (the reshim of mise's wrapper failed with "mise: command not found").
func TestIsolatedNpmBinBypassesVersionManagerWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the npm toolchain of the real-npm test is POSIX only")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	cli := filepath.Join(root, "lib", "node_modules", "npm", "bin")
	for _, dir := range []string{bin, cli} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	testenv.Symlink(t, node, filepath.Join(bin, "node"))
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\necho 'mise: command not found' >&2\nexit 127\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cli, "npm-cli.js"), []byte("process.stdout.write('npm-cli ' + process.argv.slice(2).join(' '))\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	isolated := isolatedNpmBin(t, filepath.Join(bin, "node"), t.TempDir())
	if isolated == bin {
		t.Fatalf("the isolated toolchain is the version manager's bin directory %s", bin)
	}
	command := exec.Command(filepath.Join(isolated, "npm"), "install", "-g", "package")
	command.Env = []string{"PATH=" + isolated + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"}
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "npm-cli install -g package" {
		t.Fatalf("isolated npm = %q, %v", output, err)
	}
	node2, err := exec.Command(filepath.Join(isolated, "node"), "-p", "1+1").Output()
	if err != nil || strings.TrimSpace(string(node2)) != "2" {
		t.Fatalf("isolated node = %q, %v", node2, err)
	}
}
