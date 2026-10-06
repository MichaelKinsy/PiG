package pigletbuild

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
	"github.com/MichaelKinsy/PiG/internal/npmpublish/npmtest"
)

// TestPublishNPMBinariesReferencePullsTheSignedRelease is the Binary half of the npm round trip: a Piglet is published
// to GitHub Releases and then to npm, and the `pig.binaries` reference that the published package.json carries pulls
// and verifies the signed Binary the author uploaded. Two owners, npm scopes, Package names, tag layouts, and ways of
// naming the release (this machine's record, and --binaries) prove that nothing in the path names an owner or scope.
func TestPublishNPMBinariesReferencePullsTheSignedRelease(t *testing.T) {
	const target = "linux/amd64"
	for _, tc := range []struct {
		name, piglet, version          string
		packageName, packageVersion    string
		npmName, repository, tagPrefix string
		explicitBinaries               bool
		wantRef, wantPackage           string
	}{
		{
			name: "recorded namespaced release", piglet: "porter", version: "1.2.3",
			packageName: "@acme/review", packageVersion: "1.2.0",
			npmName: "@acme/porter", repository: "acme/porter", tagPrefix: "porter/",
			wantRef: "github:acme/porter/porter@1.2.3", wantPackage: "npm:@acme/review@^1.2.0",
		},
		{
			name: "explicit unprefixed release", piglet: "hauler", version: "2.0.0",
			packageName: "zeta-tools", packageVersion: "3.1.0",
			npmName: "@zeta-labs/hauler", repository: "zeta-labs/agents", explicitBinaries: true,
			wantRef: "github:zeta-labs/agents@2.0.0", wantPackage: "npm:zeta-tools@^3.1.0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh, _ := publishTestEnv(t)
			fake := npmtest.Install(t)
			fake.Seed(tc.packageName, tc.packageVersion)
			installresolver.SetMaterializer(func(_, source, _ string, _, _ io.Writer) (string, error) { return source, nil })
			t.Cleanup(func() { installresolver.SetMaterializer(nil) })
			client := githubReleaseClient(t, gh, tc.repository)
			source := writeNamedNPMPiglet(t, tc.piglet, tc.version, `{"name":"`+tc.packageName+`","version":"`+tc.packageVersion+`","pi":{"extensions":["extensions/*"]}}`)
			keyPath, _, keyID := writePublishKey(t)

			github := []string{source, "--to", "github", "--repo", tc.repository, "--sign-key", keyPath, "--targets", target, "--yes"}
			if tc.tagPrefix != "" {
				github = append(github, "--tag-prefix", tc.tagPrefix)
			}
			if code, stdout, stderr := runPublishForTest(t, newSigningFakeBuilder(t).builders, github...); code != 0 {
				t.Fatalf("publish --to github: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if tc.explicitBinaries {
				// Without this machine's record the release must be read from GitHub.
				path, err := githubPublicationPath(tc.piglet, tc.version)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			npm := []string{source, "--to", "npm", "--yes", "--access", "public", "--npm-name", tc.npmName}
			if tc.explicitBinaries {
				npm = append(npm, "--binaries", "github:"+tc.repository)
			}
			if code, stdout, stderr := runNPMPublish(t, npmPublishDeps{client: client}, npm...); code != 0 {
				t.Fatalf("publish --to npm: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}

			pig, _ := fake.Manifest(tc.npmName, tc.version)["pig"].(map[string]any)
			binaries, _ := pig["binaries"].(map[string]any)
			ref, _ := binaries["ref"].(string)
			signer, _ := binaries["signer"].(string)
			if pig["piglet"] != "piglet.yaml" || ref != tc.wantRef || signer != keyID {
				t.Fatalf("pig = %v; want piglet.yaml, ref %s, signer %s", pig, tc.wantRef, keyID)
			}
			published, err := os.ReadFile(filepath.Join(fake.PackageDir(tc.npmName, tc.version), "piglet.yaml"))
			if err != nil || !strings.Contains(string(published), tc.wantPackage) || strings.Contains(string(published), "local:./pkgs") {
				t.Fatalf("published piglet.yaml = %s, %v; want %s", published, err, tc.wantPackage)
			}

			result, err := pigletrelease.Pull(t.Context(), ref, pigletrelease.Options{Client: client, Target: target})
			if err != nil {
				t.Fatalf("pull %s: %v", ref, err)
			}
			if result.Piglet != tc.piglet || result.Version != tc.version || result.Target != target || result.SignerKeyID != signer {
				t.Fatalf("pull = %+v; want %s %s %s signed by %s", result, tc.piglet, tc.version, target, signer)
			}
			pulled, err := os.ReadFile(result.Artifact)
			if err != nil || !strings.HasPrefix(string(pulled), "fake Piglet Binary for "+target+"\n") {
				t.Fatalf("pulled artifact = %q, %v", pulled, err)
			}
		})
	}
}

// githubReleaseClient serves the assets the fake gh uploaded for repository at GitHub's release download URLs.
func githubReleaseClient(t *testing.T, gh fakeGH, repository string) *http.Client {
	t.Helper()
	prefix := "/" + repository + "/releases/download/"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ok := strings.CutPrefix(r.URL.EscapedPath(), prefix)
		tag, asset, found := strings.Cut(path, "/")
		if !ok || !found || strings.Contains(asset, "/") {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(gh.state, "releases", tag, asset))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Transport = githubTestTransport{endpoint: endpoint, transport: client.Transport}
	return client
}

// writeNamedNPMPiglet writes a released Piglet with one local Package that has the given package.json.
func writeNamedNPMPiglet(t *testing.T, name, version, packageJSON string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"pkgs/tools/package.json":               packageJSON,
		"pkgs/tools/extensions/runner/index.js": "export default function extension(pi) {}\n",
		"LICENSE":                               "MIT\n",
		name + ".yaml":                          "name: " + name + "\nrelease:\n  version: " + version + "\npackages:\n  tools: local:./pkgs/tools\nextensions:\n  - name: runner\n    origins: [package:tools]\n",
	}
	for relative, body := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, name+".yaml")
}

// A Piglet whose Packages are not on npm names every missing Package, not only the first, and publishes nothing.
func TestPublishNPMNamesEveryUnpublishedPackage(t *testing.T) {
	fake, tmp := npmPublishEnv(t)
	source := writeNPMPiglet(t, `{"name":"@north-co/review","version":"2.0.0","pi":{"extensions":["extensions/*"]}}`)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	two := strings.Replace(string(data), "packages:\n", "packages:\n  helpers: npm:south-helpers@^0.3.0\n", 1)
	if err := os.WriteFile(source, []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm", "--yes")

	for _, want := range []string{`Package "helpers" (npm:south-helpers@^0.3.0) is not on npm`, `Package "review" (npm:@north-co/review@^2.0.0) is not on npm`} {
		if code != 1 || !strings.Contains(stderr, want) {
			t.Fatalf("code=%d stderr=%s\nwant %q", code, stderr, want)
		}
	}
	if len(fake.Publishes()) != 0 {
		t.Fatalf("published: %v", fake.Publishes())
	}
	assertNoStaging(t, tmp)
}

// A local Package that publishes to its own registry keeps that registry in the published Piglet. The precheck asks npm's
// configured registry, which does not have that Package, so it skips a registry-qualified reference instead of refusing.
func TestPublishNPMKeepsAPackagesOwnRegistryAndSkipsItsPrecheck(t *testing.T) {
	fake, _ := npmPublishEnv(t)
	source := writeNPMPiglet(t, `{"name":"@north-co/review","version":"2.0.0","publishConfig":{"registry":"https://npm.pkg.github.com/"},"pi":{"extensions":["extensions/*"]}}`)

	code, stdout, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm", "--yes")

	const want = "npm:@north-co/review@^2.0.0?registry=https%3A%2F%2Fnpm.pkg.github.com"
	if code != 0 || !strings.Contains(stdout, "Package review: "+want+"\n") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, call := range fake.Calls() {
		if len(call.Args) > 1 && call.Args[0] == "view" && strings.HasPrefix(call.Args[1], "@north-co/review") {
			t.Fatalf("the precheck asked the default registry for a registry-qualified Package: %v", call.Args)
		}
	}
	published, err := os.ReadFile(filepath.Join(fake.PackageDir("porter", "1.2.3"), "piglet.yaml"))
	if err != nil || !strings.Contains(string(published), "review: "+want) {
		t.Fatalf("published piglet.yaml = %s, %v", published, err)
	}
}
