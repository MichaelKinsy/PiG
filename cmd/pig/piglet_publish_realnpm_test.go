package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec // npm's packument carries a SHA-1 shasum beside the SHA-512 integrity it verifies.
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/pigletbuild"
	"github.com/MichaelKinsy/PiG/internal/npmpublish"
)

// tarballRegistry is a read-only npm registry over tarballs made by `npm pack`. It serves the packument and tarball
// URLs the real npm CLI requests, so `npm view`, `npm publish --dry-run`, and `npm install` run unmodified.
type tarballRegistry struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	versions map[string]map[string]map[string]any
	tarballs map[string][]byte
}

func newTarballRegistry(t *testing.T) *tarballRegistry {
	t.Helper()
	r := &tarballRegistry{t: t, versions: map[string]map[string]map[string]any{}, tarballs: map[string][]byte{}}
	r.server = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.server.Close)
	return r
}

func (r *tarballRegistry) serve(w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path, err := url.PathUnescape(request.URL.EscapedPath())
	if err != nil || request.Method != http.MethodGet {
		http.NotFound(w, request)
		return
	}
	if file, ok := strings.CutPrefix(path, "/-/"); ok {
		if data, found := r.tarballs[file]; found {
			_, _ = w.Write(data)
			return
		}
	} else if versions, found := r.versions[strings.TrimPrefix(path, "/")]; found {
		latest := ""
		for version := range versions {
			if latest == "" || semver.Compare("v"+version, "v"+latest) > 0 {
				latest = version
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": strings.TrimPrefix(path, "/"), "dist-tags": map[string]string{"latest": latest}, "versions": versions})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"error":"Not found"}`)
}

// pack runs the real `npm pack` on dir, which makes the tarball `npm publish` would upload, and adds it to the registry.
func (r *tarballRegistry) pack(dir string) map[string]any {
	r.t.Helper()
	out := r.t.TempDir()
	if output, err := exec.Command("npm", "pack", dir, "--pack-destination", out, "--ignore-scripts").CombinedOutput(); err != nil {
		r.t.Fatalf("npm pack %s: %v\n%s", dir, err, output)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 {
		r.t.Fatalf("npm pack wrote %v, %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(out, entries[0].Name()))
	if err != nil {
		r.t.Fatal(err)
	}
	manifest := tarballManifest(r.t, data)
	sha := sha1.Sum(data) //nolint:gosec // see the import.
	integrity := sha512.Sum512(data)
	manifest["dist"] = map[string]string{
		"tarball":   r.server.URL + "/-/" + entries[0].Name(),
		"shasum":    hex.EncodeToString(sha[:]),
		"integrity": "sha512-" + base64.StdEncoding.EncodeToString(integrity[:]),
	}
	name, _ := manifest["name"].(string)
	version, _ := manifest["version"].(string)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tarballs[entries[0].Name()] = data
	if r.versions[name] == nil {
		r.versions[name] = map[string]map[string]any{}
	}
	r.versions[name][version] = manifest
	return manifest
}

func tarballManifest(t *testing.T, data []byte) map[string]any {
	t.Helper()
	archive, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(archive)
	for {
		header, err := reader.Next()
		if err != nil {
			t.Fatalf("no package/package.json in the tarball: %v", err)
		}
		if header.Name != "package/package.json" {
			continue
		}
		var manifest map[string]any
		if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
			t.Fatal(err)
		}
		return manifest
	}
}

// TestPigletNPMSourceRoundTripsThroughRealNPM checks publication against the real npm CLI rather than the stand-in:
// npm's own 404 output for an unpublished Package and version, the dry run of `pig piglet publish --to npm` and
// `pig package publish --to npm`, the tarball `npm pack` makes from the generated source, and `pig piglet add npm:`
// installing that tarball and its Packages from a registry. Two npm scopes and an unscoped Package keep names out of the
// code path. Nothing is published: the registry serves only what `npm pack` produced.
func TestPigletNPMSourceRoundTripsThroughRealNPM(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Fatalf("npm is required: %v", err)
	}
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	t.Setenv(npmpublish.TrustedPublishingEnv, "")
	registry := newTarballRegistry(t)
	t.Setenv("npm_config_registry", registry.server.URL+"/")
	t.Setenv("npm_config_cache", t.TempDir())
	t.Setenv("npm_config_userconfig", filepath.Join(t.TempDir(), "npmrc"))
	t.Setenv("npm_config_update_notifier", "false")
	t.Chdir(t.TempDir())

	author := t.TempDir()
	for name, body := range map[string]string{
		"pkgs/review/package.json":               `{"name":"@alpha-co/review","version":"1.2.0","description":"Review tools","keywords":["pig-package"],"license":"MIT","pi":{"extensions":["extensions/runner"]}}`,
		"pkgs/review/extensions/runner/index.js": "export default function extension(pi) {}\n",
		"pkgs/helpers/package.json":              `{"name":"zeta-helpers","version":"0.3.1","description":"Helper skills","keywords":["pig-package"],"license":"MIT","pi":{"skills":["skills/haul"]}}`,
		"pkgs/helpers/skills/haul/SKILL.md":      "---\nname: haul\ndescription: Haul things\n---\nHaul.\n",
		"porter.yaml":                            "name: porter\ndescription: Hauls things\nrelease:\n  version: 1.0.0\nsystemPrompt:\n  file: prompts/system.md\npackages:\n  review: local:./pkgs/review\n  helpers: local:./pkgs/helpers\nextensions:\n  - name: runner\n    origins: [package:review]\nskills:\n  - name: haul\n    origins: [package:helpers]\n",
		"prompts/system.md":                      "You haul things.\n",
		"package.json":                           `{"name":"@beta-org/porter","license":"MIT","scripts":{"postinstall":"exit 1"}}`,
		"LICENSE":                                "MIT\n",
	} {
		path := filepath.Join(author, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pigletPath := filepath.Join(author, "porter.yaml")
	npm := []string{"npm"}
	ctx := context.Background()

	// npm's real 404 for an unknown name and for an unknown version both read as "not published".
	if ok, err := npmpublish.Satisfiable(ctx, npm, "@alpha-co/review", "^1.2.0", ""); err != nil || ok {
		t.Fatalf("Satisfiable before publication = %v, %v", ok, err)
	}

	// The dry run uses the real npm and names both unpublished Packages; --yes refuses.
	var stdout, stderr strings.Builder
	if code := pigletbuild.RunPigletPublishCommand([]string{pigletPath, "--to", "npm"}, &stdout, &stderr); code != 0 {
		t.Fatalf("dry run: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"Piglet: @beta-org/porter@1.0.0 (not on npm yet)",
		`Warning: Package "helpers" (npm:zeta-helpers@^0.3.1) is not on npm`,
		`Warning: Package "review" (npm:@alpha-co/review@^1.2.0) is not on npm`,
		"Dry run only; rerun with --yes to publish.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("dry run lacks %q:\n%s", want, stdout.String())
		}
	}
	if !strings.Contains(stderr.String(), "piglet.yaml") || !strings.Contains(stderr.String(), "prompts/system.md") {
		t.Fatalf("npm's dry-run listing lacks the generated files:\n%s", stderr.String())
	}

	// Each Package's helper dry run passes with the real npm; the registry then receives what npm packs.
	for _, dir := range []string{"pkgs/review", "pkgs/helpers"} {
		if code, out, errOut := runPackagePublishForTest(t, filepath.Join(author, dir), "--to", "npm"); code != 0 {
			t.Fatalf("package dry run %s: code=%d stdout=%s stderr=%s", dir, code, out, errOut)
		}
		registry.pack(filepath.Join(author, dir))
	}
	if ok, err := npmpublish.Satisfiable(ctx, npm, "@alpha-co/review", "^1.2.0", ""); err != nil || !ok {
		t.Fatalf("Satisfiable after publication = %v, %v", ok, err)
	}
	if ok, err := npmpublish.Satisfiable(ctx, npm, "@alpha-co/review", "9.9.9", ""); err != nil || ok {
		t.Fatalf("Satisfiable for an unpublished version = %v, %v", ok, err)
	}

	// The generated source, packed by npm, is what `pig piglet add npm:` installs.
	source, err := piglet.BuildNPMSource(piglet.NPMSourceOptions{Path: pigletPath, Workspace: author})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(source.Dir) }()
	manifest := registry.pack(source.Dir)
	if pig, _ := manifest["pig"].(map[string]any); pig["piglet"] != "piglet.yaml" || manifest["scripts"] != nil || manifest["name"] != "@beta-org/porter" {
		t.Fatalf("packed package.json = %v", manifest)
	}

	stdout.Reset()
	stderr.Reset()
	if code := piglet.RunCommand([]string{"piglet", "add", "npm:@beta-org/porter", "--no-input"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Added porter (1 extensions, 1 skills)") {
		t.Fatalf("add output: %s", stdout.String())
	}
	registered, err := os.ReadFile(filepath.Join(home, "piglets", "porter.yaml"))
	if err != nil || !strings.Contains(string(registered), "review: npm:@alpha-co/review@^1.2.0") || !strings.Contains(string(registered), "helpers: npm:zeta-helpers@^0.3.1") {
		t.Fatalf("registered Piglet = %s, %v", registered, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := piglet.RunCommand([]string{"piglet", "validate", "porter"}, &stdout, &stderr); code != 0 {
		t.Fatalf("validate: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	// The same name and version is now refused, before npm publish runs.
	stdout.Reset()
	stderr.Reset()
	if code := pigletbuild.RunPigletPublishCommand([]string{pigletPath, "--to", "npm", "--yes"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "@beta-org/porter@1.0.0 is already on npm") {
		t.Fatalf("republish: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
